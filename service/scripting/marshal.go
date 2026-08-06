package scripting

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Input struct {
	Request           RequestInput
	Vars              map[string]any
	Secrets           map[string]any
	Env               map[string]any
	Args              []any
	Params            map[string]any
	InheritedMetadata map[string][]string
}

type RequestInput struct {
	Body     any               `json:"body"`
	Metadata map[string]string `json:"metadata"`
	Target   string            `json:"target"`
}

type Result struct {
	Value json.RawMessage
	Logs  []LogLine
}

type LogLine struct {
	Level   string
	Message string
}

type logCollector struct {
	lines []LogLine
}

func (c *logCollector) add(level, msg string) {
	c.lines = append(c.lines, LogLine{Level: level, Message: msg})
}

func levelName(l int32) string {
	switch l {
	case 0:
		return "debug"
	case 2:
		return "warn"
	case 3:
		return "error"
	default:
		return "log"
	}
}

// `var` plus a globalThis assignment, so re-evaluating this in a reused context cannot redeclare.
const preludeHelpers = `var __ff=function f(o){if(o&&typeof o==="object"){` +
	`if(Array.isArray(o)){for(var i=0;i<o.length;i++)f(o[i]);}else{for(var k in o)f(o[k]);}` +
	`Object.freeze(o);}return o;};` +
	`globalThis.console=(function(){var fmt=function(x){var t=typeof x;` +
	`if(t==="string")return x;` +
	`if(t==="number"||t==="boolean"||t==="bigint")return String(x);` +
	`if(t==="undefined")return "undefined";` +
	`if(t==="function")return "[Function]";` +
	`if(t==="symbol")return x.toString();` +
	`try{return JSON.stringify(x)}catch(e){return String(x)}};` +
	`var w=function(l){return function(){` +
	`var a=Array.prototype.slice.call(arguments).map(fmt).join(" ");` +
	`globalThis.__grpcview_console(l,a);};};` +
	`return{debug:w(0),log:w(1),info:w(1),warn:w(2),error:w(3)};})();` + "\n"

func buildInputPrelude(in Input) string {
	var b strings.Builder
	b.WriteString(preludeHelpers)
	b.WriteString(netFetchPrelude)

	req := in.Request
	if req.Metadata == nil {
		req.Metadata = map[string]string{}
	}
	writeGlobal(&b, "request", req)
	writeGlobal(&b, "vars", orEmptyMap(in.Vars))
	writeGlobal(&b, "secrets", orEmptyMap(in.Secrets))
	writeGlobal(&b, "env", orEmptyMap(in.Env))
	b.WriteString(buildGvPrelude(in))
	return b.String()
}

// JSON-encodes v, then JSON-encodes that JSON to a string literal: a JSON string is also a valid JS
// string literal, so the payload cannot break out of it.
func jsonLit(v any) string {
	jsonBytes, err := json.Marshal(v)
	if err != nil {
		jsonBytes = []byte("null")
	}
	lit, _ := json.Marshal(string(jsonBytes))
	return string(lit)
}

func writeGlobal(b *strings.Builder, name string, v any) {
	fmt.Fprintf(b, "globalThis.%s = __ff(JSON.parse(%s));\n", name, jsonLit(v))
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func orEmptyMetadata(m map[string][]string) map[string][]string {
	if m == nil {
		return map[string][]string{}
	}
	return m
}

const gvInvokeShim = `function (path, params) {
  try {
    var req = JSON.stringify({ path: String(path), params: (params == null ? {} : params) });
    return Promise.resolve(JSON.parse(globalThis.__grpcview_invoke(req)));
  } catch (e) {
    return Promise.reject(e);
  }
}`

// `gv` must be assembled and frozen in ONE statement: the freeze blocks later member addition, and a
// second assignment would clobber it. The inherited map needs its own __ff because it hangs off the
// inherit() closure, not gv's property graph.
func buildGvPrelude(in Input) string {
	data := map[string]any{
		"request": map[string]any{"params": orEmptyMap(in.Params)},
	}
	return fmt.Sprintf(`globalThis.gv = __ff((function () {
  var d = JSON.parse(%s);
  var m = __ff(JSON.parse(%s));
  d.metadata = { inherit: function () { return m; } };
  d.invoke = %s;
  return d;
})());
`, jsonLit(data), jsonLit(orEmptyMetadata(in.InheritedMetadata)), gvInvokeShim)
}

func decodeResult(tag uint8, payload []byte) (json.RawMessage, error) {
	switch tag {
	case tagThrow:
		return nil, parseJSError(payload)
	case tagUndefined:
		return nil, nil
	case tagValue:
		return json.RawMessage(payload), nil
	default:
		return nil, fmt.Errorf("scripting: unknown result tag %d", tag)
	}
}

var stackLineRe = regexp.MustCompile(`:(\d+)`)

func parseJSError(payload []byte) *JSError {
	msg, stack, _ := strings.Cut(string(payload), "\n")
	e := &JSError{Message: msg, Stack: stack}
	if m := stackLineRe.FindStringSubmatch(stack); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			e.Line = n
		}
	}
	return e
}

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
	Args              []any
	Params            map[string]any
	InheritedMetadata map[string][]string
	// Absolute path of the compiling script's collection; empty disables `~/` imports.
	CollectionRoot string
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
	writeDataGlobal(&b, "__grpcview_request", req)
	writeDataGlobal(&b, "__grpcview_params", orEmptyMap(in.Params))
	writeDataGlobal(&b, "__grpcview_inherited", orEmptyMetadata(in.InheritedMetadata))
	// The prelude and the author's code are ONE program, so the program's completion value falls
	// back to the prelude's last expression when the author's code contributes none. Ending on a
	// statement with no useful value keeps the prelude from being reported as a scratchpad's answer.
	b.WriteString("void 0;\n")
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

func writeDataGlobal(b *strings.Builder, name string, v any) {
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

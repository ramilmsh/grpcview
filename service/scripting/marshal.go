package scripting

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Input is the structured, read-only data a script sees as frozen globals. Each field
// is serialized Go -> JSON and re-parsed inside the guest, so the script only ever
// touches a copied, inert POJO — no Go object-graph is reachable across the boundary.
type Input struct {
	Request RequestInput   // globalThis.request {body, metadata, target}
	Vars    map[string]any // globalThis.vars    — resolved variables
	Secrets map[string]any // globalThis.secrets — resolved secrets
	Env     map[string]any // globalThis.env     — environment values
	// Args are the positional arguments a GENERATOR's exported entry point is called
	// with. Ignored by middleware and scenario/scratchpad runs, which take no
	// positional args.
	Args []any
}

// RequestInput mirrors the gRPC request a middleware/generator script operates on.
type RequestInput struct {
	Body     any               `json:"body"`     // the request message (arbitrary JSON) or null
	Metadata map[string]string `json:"metadata"` // request metadata / headers
	Target   string            `json:"target"`   // the call target (host:port / URL)
}

// Result is a script run's structured outcome: the return value as raw JSON (nil when
// the script returned undefined / a non-serializable top-level value — a JSON `null`
// return is []byte("null"), distinct from nil) plus the buffered console output.
type Result struct {
	Value json.RawMessage
	Logs  []LogLine
}

// LogLine is one buffered console call: its level and the formatted message.
type LogLine struct {
	Level   string // debug | log | info(->log) | warn | error
	Message string
}

// logCollector buffers console output for one run. An Instance is never used
// concurrently and wazero host functions run on the calling goroutine, so no lock is
// needed.
type logCollector struct {
	lines []LogLine
}

func (c *logCollector) add(level, msg string) {
	c.lines = append(c.lines, LogLine{Level: level, Message: msg})
}

// levelName maps the console level ordinal (set in the prelude's console shim) to a
// name. 1 (log) is the default for anything unexpected.
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

// preludeHelpers defines the deep-freeze helper and the console object the injected
// inputs and scripts use. Written with `var` + globalThis assignment so it is safe to
// re-evaluate in a long-lived (reused) context without a redeclaration error.
const preludeHelpers = `var __ff=function f(o){if(o&&typeof o==="object"){` +
	`if(Array.isArray(o)){for(var i=0;i<o.length;i++)f(o[i]);}else{for(var k in o)f(o[k]);}` +
	`Object.freeze(o);}return o;};` +
	`globalThis.console=(function(){var fmt=function(x){var t=typeof x;` +
	`if(t==="string")return x;` +
	`if(t==="number"||t==="boolean"||t==="bigint")return String(x);` + // NaN/Infinity/true render honestly
	`if(t==="undefined")return "undefined";` +
	`if(t==="function")return "[Function]";` +
	`if(t==="symbol")return x.toString();` +
	`try{return JSON.stringify(x)}catch(e){return String(x)}};` + // null->"null"; objects->JSON; cyclic->String
	`var w=function(l){return function(){` +
	`var a=Array.prototype.slice.call(arguments).map(fmt).join(" ");` +
	`globalThis.__grpcview_console(l,a);};};` +
	`return{debug:w(0),log:w(1),info:w(1),warn:w(2),error:w(3)};})();` + "\n"

// buildInputPrelude renders the prelude that installs the console + the frozen input
// globals. It is prepended to the bundled user source. Inputs are assigned to
// globalThis (writable bindings), so re-running in a reused context overwrites them
// cleanly; the objects themselves are deep-frozen so a script cannot mutate its inputs.
func buildInputPrelude(in Input) string {
	var b strings.Builder
	b.WriteString(preludeHelpers)

	req := in.Request
	if req.Metadata == nil {
		req.Metadata = map[string]string{} // so scripts can index request.metadata safely
	}
	writeGlobal(&b, "request", req)
	writeGlobal(&b, "vars", orEmptyMap(in.Vars))
	writeGlobal(&b, "secrets", orEmptyMap(in.Secrets))
	writeGlobal(&b, "env", orEmptyMap(in.Env))
	return b.String()
}

// writeGlobal emits `globalThis.<name> = __ff(JSON.parse(<literal>));`. The value is
// JSON-encoded, then that JSON is itself JSON-encoded to a string literal — a JSON
// string is also a valid JS string literal, so JSON.parse reconstructs the value with
// no risk of the payload breaking out of the literal.
func writeGlobal(b *strings.Builder, name string, v any) {
	jsonBytes, err := json.Marshal(v)
	if err != nil {
		jsonBytes = []byte("null")
	}
	lit, _ := json.Marshal(string(jsonBytes))
	fmt.Fprintf(b, "globalThis.%s = __ff(JSON.parse(%s));\n", name, lit)
}

func orEmptyMap(m map[string]any) any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// decodeResult turns a raw result envelope (from qjs_result in JSON mode) into a value
// or a *JSError.
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

// stackLineRe pulls the first ":<digits>" out of a guest backtrace — the raw <script>
// line of the topmost frame (e.g. "    at <script>:3:5" -> 3). Best-effort; 0 if none.
var stackLineRe = regexp.MustCompile(`:(\d+)`)

// parseJSError splits a throw payload ("message" or "message\nstack") into a *JSError
// and parses the first source line out of the stack.
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

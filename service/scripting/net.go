package scripting

// net.go — the network capability: a browser-style global `fetch` for every script.
//
// Network access is UNCONDITIONAL. Every script (request body, generator, middleware,
// scenario) gets a global `fetch` and it always works — there is no per-run grant and
// no capability manager (a deliberate, temporary simplification). `fetch` is injected as
// an ambient global by the prelude (netFetchPrelude), not imported, so no `import` and no
// Gate-1 wiring is involved; the only host boundary is the __grpcview_net_fetch bridge
// qjs_wasm.c registers, backed by hostNetFetch below.
//
// The bridge is SYNCHRONOUS: the JS shim marshals a request envelope to one string,
// hostNetFetch performs the whole HTTP round trip on the calling goroutine, and the
// response envelope comes back as the call's value (a rejected request comes back as a
// throw the guest re-raises). Blocking is safe here because each run owns its instance +
// goroutine and the run's context deadline (the profile wall-clock budget) is threaded
// into the request, so a slow endpoint is cut off rather than hanging the process. This
// trades the async ticket design (see the capabilities spike doc) for far less machinery;
// the guest never observes the difference because the shim hands back a resolved Promise.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tetratelabs/wazero/api"
)

// maxNetResponseBytes caps how much of a response body is read into memory, so a hostile
// or accidental huge response cannot balloon host (and then guest) memory. 10 MiB is ample
// for the token/JSON endpoints scripts realistically call; a larger body fails the fetch.
const maxNetResponseBytes = 10 << 20

// netClient is the shared HTTP client for script fetch(). It carries NO client-level
// timeout on purpose: each request is bounded by the run's context deadline (threaded in
// via http.NewRequestWithContext), so the profile's wall-clock budget is the single source
// of truth for how long a fetch may take.
var netClient = &http.Client{}

// netRequest is the fetch() request envelope the JS shim (netFetchPrelude) marshals into
// the single string argument of __grpcview_net_fetch.
type netRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"` // nil => no request body
}

// netResponse is the envelope returned to the JS shim, which reconstructs a Response-like
// object from it. Header keys are lowercased and multi-valued headers comma-joined to match
// the Fetch API's case-insensitive, comma-joined Headers.get.
type netResponse struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	URL        string            `json:"url"` // final URL after redirects
}

// netFetchPrelude installs the global `fetch`. It is prepended to every run (buildInputPrelude)
// and written with a globalThis assignment so re-evaluating it in a reused (long-lived)
// context cannot raise a redeclaration error. fetch marshals a netRequest, makes the one
// synchronous host call, and hands back a resolved Promise<Response>; any failure (bad input,
// transport error, host throw) becomes a REJECTED promise — fetch never throws synchronously,
// matching the Fetch API so `.catch` works.
const netFetchPrelude = `globalThis.fetch=(function(){
function headersOf(h){var out={};if(!h)return out;
if(typeof h.forEach==="function"&&!Array.isArray(h)){h.forEach(function(v,k){out[String(k)]=String(v);});return out;}
if(Array.isArray(h)){for(var i=0;i<h.length;i++){out[String(h[i][0])]=String(h[i][1]);}return out;}
for(var k in h){out[k]=String(h[k]);}return out;}
function makeResponse(r){var hdrs=r.headers||{};
var headers={get:function(n){var v=hdrs[String(n).toLowerCase()];return v==null?null:v;},
has:function(n){return Object.prototype.hasOwnProperty.call(hdrs,String(n).toLowerCase());},
forEach:function(cb){for(var k in hdrs)cb(hdrs[k],k);}};
var body=r.body==null?"":r.body;
return{ok:r.status>=200&&r.status<300,status:r.status,statusText:r.statusText||"",url:r.url||"",headers:headers,
text:function(){return Promise.resolve(body);},
json:function(){try{return Promise.resolve(JSON.parse(body));}catch(e){return Promise.reject(e);}}};}
return function fetch(input,init){try{init=init||{};
var url=(input&&typeof input==="object"&&input.url!=null)?String(input.url):String(input);
var req={method:init.method?String(init.method).toUpperCase():"GET",url:url,headers:headersOf(init.headers),
body:init.body==null?null:String(init.body)};
return Promise.resolve(makeResponse(JSON.parse(globalThis.__grpcview_net_fetch(JSON.stringify(req)))));
}catch(e){return Promise.reject(e);}};})();
` + "\n"

// hostNetFetch is the Go end of the __grpcview_net_fetch bridge: read the request envelope
// out of guest memory, perform the request, and write the response envelope back — or write
// a throw the guest re-raises as a catchable JS exception (a rejected fetch promise). See the
// file header for why the blocking round trip is safe.
func hostNetFetch(ctx context.Context, mod api.Module, stack []uint64) {
	raw, ok := mod.Memory().Read(uint32(stack[0]), uint32(stack[1]))
	if !ok {
		stack[0] = uint64(writeResult(ctx, mod, tagThrow, []byte("fetch: bad request pointer")))
		return
	}
	payload, err := doNetFetch(ctx, raw)
	if err != nil {
		stack[0] = uint64(writeResult(ctx, mod, tagThrow, []byte("fetch: "+err.Error())))
		return
	}
	stack[0] = uint64(writeResult(ctx, mod, tagValue, payload))
}

// doNetFetch parses the request envelope, performs the request under ctx, and marshals the
// response envelope. It is split from hostNetFetch so it is unit-testable without a wasm
// module. reqJSON aliases guest memory, so it is unmarshalled before any call that could
// grow/move that memory.
func doNetFetch(ctx context.Context, reqJSON []byte) ([]byte, error) {
	var r netRequest
	if err := json.Unmarshal(reqJSON, &r); err != nil {
		return nil, fmt.Errorf("bad request: %w", err)
	}
	if r.Method == "" {
		r.Method = http.MethodGet
	}
	var body io.Reader
	if r.Body != nil {
		body = strings.NewReader(*r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		return nil, err
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	resp, err := netClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read one byte past the cap so an at-the-limit body is distinguishable from an
	// over-limit one, then reject the latter rather than silently truncating.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxNetResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxNetResponseBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxNetResponseBytes)
	}

	headers := make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		headers[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	out := netResponse{
		Status:     resp.StatusCode,
		StatusText: statusText(resp.Status),
		Headers:    headers,
		Body:       string(data),
		URL:        resp.Request.URL.String(),
	}
	return json.Marshal(out)
}

// statusText extracts the reason phrase from an http.Response.Status ("200 OK" -> "OK").
func statusText(status string) string {
	if i := strings.IndexByte(status, ' '); i >= 0 {
		return status[i+1:]
	}
	return status
}

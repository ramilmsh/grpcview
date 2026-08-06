package scripting

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tetratelabs/wazero/api"
)

const maxNetResponseBytes = 10 << 20

var netClient = &http.Client{}

type netRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"`
}

type netResponse struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	URL        string            `json:"url"`
}

// Installs the global `fetch` as a globalThis assignment, so re-evaluating it in a reused context
// cannot raise a redeclaration error, and it never throws synchronously. The host call is SYNCHRONOUS
// and bounded by the run's ctx deadline — no client-level timeout, on purpose.
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

// reqJSON aliases guest memory, so it is unmarshalled before anything can move that memory.
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

func statusText(status string) string {
	if i := strings.IndexByte(status, ' '); i >= 0 {
		return status[i+1:]
	}
	return status
}

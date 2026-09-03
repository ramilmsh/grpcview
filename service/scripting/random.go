package scripting

import (
	"context"
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"

	"github.com/tetratelabs/wazero/api"
)

// A body-filling script has no legitimate need for more than a few KB of entropy at once.
const maxRandomBytes = 4096

// Installs `crypto.getRandomValues`, a REAL entropy source, deliberately separate from
// Math.random()/Date.now() which stay deterministic per sandbox instance (see engine.go's
// doc on that) so existing reproducible-output scripts keep working. Assigned via globalThis
// so re-evaluating this in a reused context cannot raise a redeclaration error.
const cryptoPrelude = `globalThis.crypto=(function(){
function getRandomValues(ta){
if(!ta||typeof ta.byteLength!=="number"||typeof ta.buffer==="undefined"){
throw new TypeError("crypto.getRandomValues: expected an integer TypedArray");}
if((typeof Float32Array!=="undefined"&&ta instanceof Float32Array)||
(typeof Float64Array!=="undefined"&&ta instanceof Float64Array)){
throw new TypeError("crypto.getRandomValues: only integer TypedArrays are supported");}
var bytes=JSON.parse(globalThis.__grpcview_random(ta.byteLength));
new Uint8Array(ta.buffer,ta.byteOffset,ta.byteLength).set(bytes);
return ta;}
return{getRandomValues:getRandomValues};})();
` + "\n"

func hostRandom(ctx context.Context, mod api.Module, stack []uint64) {
	n := int32(stack[0])
	if n < 0 || n > maxRandomBytes {
		msg := fmt.Sprintf("crypto.getRandomValues: byte count %d exceeds limit of %d", n, maxRandomBytes)
		stack[0] = uint64(writeResult(ctx, mod, tagThrow, []byte(msg)))
		return
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		stack[0] = uint64(writeResult(ctx, mod, tagThrow, []byte("crypto.getRandomValues: "+err.Error())))
		return
	}
	stack[0] = uint64(writeResult(ctx, mod, tagValue, encodeByteArray(buf)))
}

// encodeByteArray renders b as a JSON array of byte values (e.g. "[12,54,199]"), the same
// convention host_net_fetch/host_invoke use for crossing the guest/host boundary as text.
func encodeByteArray(b []byte) []byte {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, v := range b {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Itoa(int(v)))
	}
	sb.WriteByte(']')
	return []byte(sb.String())
}

package scripting

// invoke.go — the gv.invoke host bridge (plan Feature 3 §"Backend changes"). It mirrors
// net.go's fetch bridge exactly in shape: the guest gvInvokeShim (marshal.go) marshals one
// request envelope string, hostInvoke performs the whole round trip on the calling
// goroutine, and the response envelope (or a throw) comes back as the call's value.
//
// Unlike fetch, the actual work — resolving a saved request by path and re-entering the
// Invoke pipeline — lives in service/workspace, which imports service/scripting; the
// reverse import would be a cycle. So hostInvoke does not perform the invoke itself: it is
// pure plumbing, pulling an Invoker (engine.go) off the context and handing it the request
// envelope bytes verbatim. It is deliberately bytes-in/bytes-out and never inspects the
// {path, params} / InvokeResult JSON shapes — those are a contract between the guest shim
// and the Invoker implementation, not something this leaf package needs to know.

import (
	"context"

	"github.com/tetratelabs/wazero/api"
)

// errNoInvoker is the message a gv.invoke call rejects with (via the gvInvokeShim's
// try/catch turning the C shim's synchronous throw into a rejected promise) when no
// Invoker rides the context. This is the default for any run that does not explicitly
// thread one in — notably the cached RunGenerator path must never see one, per the
// cache-soundness invariant in docs/design/gv-features-plan.md.
const errNoInvoker = "invoke is not available in this context"

// hostInvoke is the Go end of the __grpcview_invoke bridge: read the {path, params}
// request envelope out of guest memory, hand it to the ctx-carried Invoker (WithInvoker,
// engine.go), and write the InvokeResult envelope back — or a throw the guest re-raises as
// a catchable JS exception, which the gvInvokeShim turns into a rejected promise. Mirrors
// hostNetFetch (net.go) in every respect but the source of the work.
func hostInvoke(ctx context.Context, mod api.Module, stack []uint64) {
	req, ok := mod.Memory().Read(uint32(stack[0]), uint32(stack[1]))
	if !ok {
		stack[0] = uint64(writeResult(ctx, mod, tagThrow, []byte("invoke: bad request pointer")))
		return
	}
	inv := invokerFromContext(ctx)
	if inv == nil {
		stack[0] = uint64(writeResult(ctx, mod, tagThrow, []byte(errNoInvoker)))
		return
	}
	// Copy req out before calling inv: it aliases guest memory (mod.Memory().Read returns a
	// view, not a copy — see callResult's identical concern in engine.go), and inv may run
	// arbitrary nested work before returning.
	reqCopy := append([]byte(nil), req...)
	resp, err := inv(ctx, reqCopy)
	if err != nil {
		stack[0] = uint64(writeResult(ctx, mod, tagThrow, []byte(err.Error())))
		return
	}
	stack[0] = uint64(writeResult(ctx, mod, tagValue, resp))
}

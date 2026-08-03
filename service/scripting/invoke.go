package scripting

// The gv.invoke host bridge: bytes-in/bytes-out plumbing to the ctx-carried Invoker.
// The invoke itself lives in service/workspace, which imports this package.

import (
	"context"

	"github.com/tetratelabs/wazero/api"
)

const errNoInvoker = "invoke is not available in this context"

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
	// req aliases guest memory, and inv may run nested work before returning.
	reqCopy := append([]byte(nil), req...)
	resp, err := inv(ctx, reqCopy)
	if err != nil {
		stack[0] = uint64(writeResult(ctx, mod, tagThrow, []byte(err.Error())))
		return
	}
	stack[0] = uint64(writeResult(ctx, mod, tagValue, resp))
}

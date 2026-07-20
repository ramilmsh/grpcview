package workspace

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
)

// RunScript evaluates an ad-hoc script through the scripting engine's scenario
// profile and returns its value, console output, and any error. Like Invoke, a
// failure of the *script itself* (a thrown exception, a timeout) is not an error
// of this RPC: it is reported in the response's Error so the UI can render it.
// Only grpcview's own inability to run the engine surfaces as a Connect error.
//
// The scratchpad runs with NO capabilities granted and NO workspace inputs — it
// is purely an end-to-end validation surface for the engine (eval, the async job
// pump, console capture, structured JSON results, and error propagation), not a
// hook into any request. Capability grants and request/vars/env inputs arrive
// when generators/middleware are wired to real inputs (next-steps §6).
func (w Workspace) RunScript(ctx context.Context, request *connect.Request[grpcviewv1.RunScriptRequest]) (*connect.Response[grpcviewv1.RunScriptResponse], error) {
	if w.engine == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("scripting engine not available"))
	}

	res, runErr := w.engine.RunScenario(ctx, request.Msg.GetSource(), scripting.Grant{}, scripting.Input{})

	out := &grpcviewv1.RunScriptResponse{}
	// Console output is returned even on failure, so a script that throws or times
	// out still surfaces what it logged before it stopped.
	for _, line := range res.Logs {
		out.Logs = append(out.Logs, &grpcviewv1.ScriptLog{Level: line.Level, Message: line.Message})
	}

	if runErr != nil {
		out.Error = scriptErrorProto(runErr)
		return connect.NewResponse(out), nil
	}

	if res.Value != nil {
		value := string(res.Value)
		out.Value = &value
	}
	return connect.NewResponse(out), nil
}

// scriptErrorProto renders an engine run error as a ScriptError for the response.
// A JavaScript exception carries its message, backtrace, and source line; the
// execution-failure sentinels (interrupt/timeout, a promise that never settles)
// carry only a message.
func scriptErrorProto(err error) *grpcviewv1.ScriptError {
	var jsErr *scripting.JSError
	if errors.As(err, &jsErr) {
		return &grpcviewv1.ScriptError{
			Message: jsErr.Message,
			Stack:   jsErr.Stack,
			Line:    int32(jsErr.Line),
		}
	}
	msg := err.Error()
	if errors.Is(err, scripting.ErrInterrupted) {
		msg = "script timed out or was interrupted"
	}
	return &grpcviewv1.ScriptError{Message: msg}
}

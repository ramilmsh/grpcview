package workspace

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
)

// RunScript evaluates a script through the scripting engine and returns its value,
// console output, and any error. Like Invoke, a failure of the *script itself* (a
// thrown exception, a timeout) is not an error of this RPC: it is reported in the
// response's Error so the UI can render it. Only grpcview's own inability to run the
// engine surfaces as a Connect error.
//
// kind selects the profile and calling convention: a generator's `export
// default` is called, a middleware's `handle`/default export is called with a ctx;
// unset (or scenario) evaluates the buffer as an ad-hoc scratchpad (last-expression
// value), unchanged. All runs pass an empty Grant (no filesystem) and NO workspace
// inputs — this is the engine's end-to-end validation + per-kind test-run surface, not
// a hook into a request. (Network `fetch` still works: it is an unconditional global,
// not a Grant capability — see scripting/net.go.) Filesystem grants and
// request/vars/env inputs arrive in later milestones.
func (w Workspace) RunScript(ctx context.Context, request *connect.Request[grpcviewv1.RunScriptRequest]) (*connect.Response[grpcviewv1.RunScriptResponse], error) {
	if w.engine == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("scripting engine not available"))
	}

	source := request.Msg.GetSource()
	var (
		res    scripting.Result
		runErr error
	)
	switch request.Msg.GetKind() {
	case grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR:
		// A generator test-run COMPOSES other generators the same way an invoke body does: load
		// the workspace's saved generators and fold in the ones this source transitively reaches,
		// so a generator under test that calls another generator resolves it. A loadGenerators
		// error is grpcview's own failure and surfaces as a Connect error; an empty reachable set
		// makes RunRequestBody take the plain generator path, so a standalone generator still
		// test-runs unchanged.
		allGens, gerr := w.loadGenerators(ctx, request.Msg.GetWorkspaceName())
		if gerr != nil {
			return nil, gerr
		}
		res, runErr = w.engine.RunRequestBody(ctx, source, transitiveGenerators(source, allGens), scripting.Grant{}, scripting.Input{})
	case grpcviewv1.ScriptKind_SCRIPT_KIND_MIDDLEWARE:
		res, runErr = w.engine.RunMiddleware(ctx, source, scripting.Grant{}, scripting.Input{})
	default: // UNSPECIFIED (scratchpad) or SCENARIO
		res, runErr = w.engine.RunScenario(ctx, source, scripting.Grant{}, scripting.Input{})
	}

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

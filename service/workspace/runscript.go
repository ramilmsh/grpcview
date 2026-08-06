package workspace

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
)

func (w Workspace) RunScript(ctx context.Context, request *connect.Request[grpcviewv1.RunScriptRequest]) (*connect.Response[grpcviewv1.RunScriptResponse], error) {
	if w.engine == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("scripting engine not available"))
	}

	source := request.Msg.GetSource()
	ctx = scripting.WithInvoker(ctx, w.scriptInvoker(request.Msg.GetCollection()))
	var (
		res    scripting.Result
		runErr error
	)
	switch request.Msg.GetKind() {
	case grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR:
		allGens, gerr := w.loadGenerators(ctx, request.Msg.GetCollection())
		if gerr != nil {
			return nil, gerr
		}
		res, runErr = w.engine.RunRequestBody(ctx, source, transitiveGenerators(source, allGens), scripting.Grant{}, scripting.Input{})
	case grpcviewv1.ScriptKind_SCRIPT_KIND_MIDDLEWARE:
		res, runErr = w.engine.RunMiddleware(ctx, source, scripting.Grant{}, scripting.Input{})
	default:
		res, runErr = w.engine.RunScenario(ctx, source, scripting.Grant{}, scripting.Input{})
	}

	out := &grpcviewv1.RunScriptResponse{}
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

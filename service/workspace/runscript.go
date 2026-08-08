package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func (w Workspace) RunScript(ctx context.Context, request *connect.Request[grpcviewv1.RunScriptRequest]) (*connect.Response[grpcviewv1.RunScriptResponse], error) {
	if w.engine == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("scripting engine not available"))
	}

	source, kind, err := w.resolveScriptToRun(ctx, request.Msg)
	if err != nil {
		return nil, err
	}

	ctx = scripting.WithInvoker(ctx, w.scriptInvoker(request.Msg.GetCollection()))
	var (
		res    scripting.Result
		runErr error
	)
	switch kind {
	case grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR:
		allGens, gerr := w.loadGenerators(ctx, request.Msg.GetCollection())
		if gerr != nil {
			return nil, gerr
		}
		res, runErr = w.engine.RunRequestBody(ctx, source, transitiveGenerators(source, allGens), scripting.Grant{}, scripting.Input{})
	case grpcviewv1.ScriptKind_SCRIPT_KIND_MIDDLEWARE:
		allGens, gerr := w.loadGenerators(ctx, request.Msg.GetCollection())
		if gerr != nil {
			return nil, gerr
		}
		res, runErr = w.engine.RunMiddleware(ctx, source, transitiveGenerators(source, allGens), scripting.Grant{}, scripting.Input{})
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

// A saved script is addressable by name from the CLI and the UI; MCP only ever had inline source,
// so running one from there meant pasting it in.
func (w Workspace) resolveScriptToRun(ctx context.Context, msg *grpcviewv1.RunScriptRequest) (string, grpcviewv1.ScriptKind, error) {
	name := strings.TrimSpace(msg.GetScript())
	hasSource := strings.TrimSpace(msg.GetSource()) != ""

	if name == "" {
		if !hasSource {
			return "", 0, connect.NewError(connect.CodeInvalidArgument,
				errors.New("nothing to run: set `source` to evaluate inline, or `script` to name a saved script"))
		}
		return msg.GetSource(), msg.GetKind(), nil
	}
	if hasSource {
		return "", 0, connect.NewError(connect.CodeInvalidArgument,
			errors.New("set `source` or `script`, not both: `script` names a saved script and carries its own kind"))
	}

	coll, err := w.store.Open(ctx, msg.GetCollection())
	if err != nil {
		return "", 0, toConnectError(err)
	}
	scripts, err := coll.Scripts(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", 0, toConnectError(err)
	}
	for _, s := range scripts {
		if s.GetName() == name {
			return s.GetSource(), s.GetKind(), nil
		}
	}
	return "", 0, connect.NewError(connect.CodeNotFound,
		fmt.Errorf("collection %q has no script named %q", msg.GetCollection(), name))
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

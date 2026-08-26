package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func (w Workspace) RunScript(ctx context.Context, request *connect.Request[grpcviewv1.RunScriptRequest]) (*connect.Response[grpcviewv1.RunScriptResponse], error) {
	if w.engine == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("scripting engine not available"))
	}

	source, err := w.resolveScriptToRun(ctx, request.Msg)
	if err != nil {
		return nil, err
	}

	coll, err := w.store.Open(ctx, request.Msg.GetCollection())
	if err != nil {
		return nil, toConnectError(err)
	}

	ctx = scripting.WithInvoker(ctx, w.scriptInvoker(request.Msg.GetCollection()))
	in := scripting.Input{CollectionRoot: coll.Root()}

	var (
		res    scripting.Result
		runErr error
	)
	if scripting.HasDefaultExport(source) {
		res, runErr = w.engine.RunRequestBody(ctx, source, scripting.Grant{}, in)
	} else {
		res, runErr = w.engine.RunScenario(ctx, source, scripting.Grant{}, in)
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

// A saved script is addressed by its collection-relative path, e.g. "scripts/uuid.ts"; MCP only
// ever had inline source, so running one from there meant pasting it in.
func (w Workspace) resolveScriptToRun(ctx context.Context, msg *grpcviewv1.RunScriptRequest) (string, error) {
	path := strings.TrimSpace(msg.GetScript())
	hasSource := strings.TrimSpace(msg.GetSource()) != ""

	if path == "" {
		if !hasSource {
			return "", connect.NewError(connect.CodeInvalidArgument,
				errors.New("nothing to run: set `source` to evaluate inline, or `script` to name a saved script"))
		}
		return msg.GetSource(), nil
	}
	if hasSource {
		return "", connect.NewError(connect.CodeInvalidArgument,
			errors.New("set `source` or `script`, not both: `script` names a saved script by its path"))
	}

	coll, err := w.store.Open(ctx, msg.GetCollection())
	if err != nil {
		return "", toConnectError(err)
	}
	scripts, err := coll.Scripts(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", toConnectError(err)
	}
	for _, s := range scripts {
		if s.GetPath() == path {
			return s.GetSource(), nil
		}
	}
	return "", connect.NewError(connect.CodeNotFound,
		fmt.Errorf("collection %q has no script at %q (`grpcview script ls` lists the paths)", msg.GetCollection(), path))
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

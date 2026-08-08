package workspace

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func createScript(t *testing.T, w Workspace, ctx context.Context, name string, kind grpcviewv1.ScriptKind, source string) {
	t.Helper()
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateScript(ctx, name, kind); err != nil {
		t.Fatalf("CreateScript %q: %v", name, err)
	}
	if err := coll.UpdateScript(ctx, name, store.ScriptPatch{Source: &source}); err != nil {
		t.Fatalf("UpdateScript %q: %v", name, err)
	}
}

func TestRunScriptRunsASavedScriptWithItsOwnKind(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	// A generator run as a scratchpad would answer with nothing: the kind has to come from the
	// saved script, not from the request.
	createScript(t, w, ctx, "answer", grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR,
		`export default () => 42`)

	resp, err := w.RunScript(ctx, connect.NewRequest(&grpcviewv1.RunScriptRequest{
		Collection: testWorkspace,
		Script:     proto.String("answer"),
	}))
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	if e := resp.Msg.GetError(); e != nil {
		t.Fatalf("script error = %q", e.GetMessage())
	}
	if resp.Msg.GetValue() != "42" {
		t.Fatalf("value = %q, want %q", resp.Msg.GetValue(), "42")
	}
}

func TestRunScriptSavedScenario(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	createScript(t, w, ctx, "smoke", grpcviewv1.ScriptKind_SCRIPT_KIND_SCENARIO,
		`gv.assert("one is one", 1 === 1);`+"\n"+`"ok"`)

	resp, err := w.RunScript(ctx, connect.NewRequest(&grpcviewv1.RunScriptRequest{
		Collection: testWorkspace,
		Script:     proto.String("smoke"),
	}))
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	if e := resp.Msg.GetError(); e != nil {
		t.Fatalf("script error = %q", e.GetMessage())
	}
	if resp.Msg.GetValue() != `"ok"` {
		t.Fatalf("value = %q, want %q", resp.Msg.GetValue(), `"ok"`)
	}
}

func TestRunScriptRejectsAnAmbiguousOrEmptyRequest(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	createScript(t, w, ctx, "saved", grpcviewv1.ScriptKind_SCRIPT_KIND_SCENARIO, `1`)

	for _, tc := range []struct {
		name string
		msg  *grpcviewv1.RunScriptRequest
		want connect.Code
	}{
		{
			name: "both source and script",
			msg:  &grpcviewv1.RunScriptRequest{Collection: testWorkspace, Source: `1`, Script: proto.String("saved")},
			want: connect.CodeInvalidArgument,
		},
		{
			name: "neither",
			msg:  &grpcviewv1.RunScriptRequest{Collection: testWorkspace},
			want: connect.CodeInvalidArgument,
		},
		{
			name: "unknown script",
			msg:  &grpcviewv1.RunScriptRequest{Collection: testWorkspace, Script: proto.String("nope")},
			want: connect.CodeNotFound,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := w.RunScript(ctx, connect.NewRequest(tc.msg))
			if err == nil {
				t.Fatalf("RunScript succeeded, want %v", tc.want)
			}
			if code := connect.CodeOf(err); code != tc.want {
				t.Fatalf("code = %v (%v), want %v", code, err, tc.want)
			}
		})
	}
}

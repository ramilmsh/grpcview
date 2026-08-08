package workspace

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func TestRunScriptRunsASavedScriptByPath(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	writeScript(t, w, ctx, "scripts/answer.ts", `export default () => 42`)

	resp, err := w.RunScript(ctx, connect.NewRequest(&grpcviewv1.RunScriptRequest{
		Collection: testWorkspace,
		Script:     proto.String("scripts/answer.ts"),
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

	writeScript(t, w, ctx, "scripts/smoke.ts",
		"import { assert } from \"grpcview:assert\";\n"+
			`assert("one is one", 1 === 1);`+"\n"+`"ok"`)

	resp, err := w.RunScript(ctx, connect.NewRequest(&grpcviewv1.RunScriptRequest{
		Collection: testWorkspace,
		Script:     proto.String("scripts/smoke.ts"),
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

	writeScript(t, w, ctx, "scripts/saved.ts", `1`)

	for _, tc := range []struct {
		name string
		msg  *grpcviewv1.RunScriptRequest
		want connect.Code
	}{
		{
			name: "both source and script",
			msg:  &grpcviewv1.RunScriptRequest{Collection: testWorkspace, Source: `1`, Script: proto.String("scripts/saved.ts")},
			want: connect.CodeInvalidArgument,
		},
		{
			name: "neither",
			msg:  &grpcviewv1.RunScriptRequest{Collection: testWorkspace},
			want: connect.CodeInvalidArgument,
		},
		{
			name: "unknown script",
			msg:  &grpcviewv1.RunScriptRequest{Collection: testWorkspace, Script: proto.String("scripts/nope.ts")},
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

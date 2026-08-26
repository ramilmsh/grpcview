package workspace

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
)

func TestScriptCRUDHandlers(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	resp, err := w.CreateScript(ctx, connect.NewRequest(&grpcviewv1.CreateScriptRequest{
		Collection: testWorkspace, Path: "scripts/uuid.ts",
	}))
	if err != nil {
		t.Fatalf("CreateScript: %v", err)
	}
	if len(resp.Msg.GetCollection().GetScripts()) != 1 {
		t.Fatalf("after first create, scripts = %v", resp.Msg.GetCollection().GetScripts())
	}
	resp, err = w.CreateScript(ctx, connect.NewRequest(&grpcviewv1.CreateScriptRequest{
		Collection: testWorkspace, Path: "scripts/sign.ts",
	}))
	if err != nil {
		t.Fatalf("CreateScript: %v", err)
	}
	scripts := resp.Msg.GetCollection().GetScripts()
	if len(scripts) != 2 || scriptByPath(scripts, "scripts/uuid.ts") == nil || scriptByPath(scripts, "scripts/sign.ts") == nil {
		t.Fatalf("scripts = %+v, want scripts/uuid.ts and scripts/sign.ts", scripts)
	}

	if _, err := w.CreateScript(ctx, connect.NewRequest(&grpcviewv1.CreateScriptRequest{
		Collection: testWorkspace, Path: "scripts/uuid.ts",
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("duplicate create code = %v, want FailedPrecondition", connect.CodeOf(err))
	}

	src := `export default () => crypto.randomUUID?.() ?? "x"`
	newPath := "scripts/uuidv4.ts"
	upd, err := w.UpdateScript(ctx, connect.NewRequest(&grpcviewv1.UpdateScriptRequest{
		Collection: testWorkspace, Path: "scripts/uuid.ts", Source: &src, NewPath: &newPath,
	}))
	if err != nil {
		t.Fatalf("UpdateScript: %v", err)
	}
	renamed := scriptByPath(upd.Msg.GetCollection().GetScripts(), "scripts/uuidv4.ts")
	if renamed == nil || renamed.GetSource() != src {
		t.Fatalf("rename+source not applied: %+v", upd.Msg.GetCollection().GetScripts())
	}

	del, err := w.DeleteScript(ctx, connect.NewRequest(&grpcviewv1.DeleteScriptRequest{
		Collection: testWorkspace, Path: "scripts/sign.ts",
	}))
	if err != nil {
		t.Fatalf("DeleteScript: %v", err)
	}
	remaining := del.Msg.GetCollection().GetScripts()
	if len(remaining) != 1 || remaining[0].GetPath() != "scripts/uuidv4.ts" {
		t.Fatalf("after delete, scripts = %+v, want [scripts/uuidv4.ts]", remaining)
	}
}

func scriptByPath(scripts []*grpcviewv1.Script, path string) *grpcviewv1.Script {
	for _, s := range scripts {
		if s.GetPath() == path {
			return s
		}
	}
	return nil
}

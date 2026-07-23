package workspace

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// TestScriptCRUDHandlers drives CreateScript/UpdateScript/DeleteScript through the RPC
// surface and asserts each returns the fresh Workspace with the expected scripts. Get is
// called first to auto-create the collection (the engine is not needed for CRUD).
func TestScriptCRUDHandlers(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()

	if _, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace})); err != nil {
		t.Fatalf("Get (auto-create): %v", err)
	}

	// Create a generator and a middleware.
	resp, err := w.CreateScript(ctx, connect.NewRequest(&grpcviewv1.CreateScriptRequest{
		WorkspaceName: testWorkspace, Name: "uuid", Kind: grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR,
	}))
	if err != nil {
		t.Fatalf("CreateScript generator: %v", err)
	}
	if len(resp.Msg.GetWorkspace().GetScripts()) != 1 {
		t.Fatalf("after first create, scripts = %v", resp.Msg.GetWorkspace().GetScripts())
	}
	resp, err = w.CreateScript(ctx, connect.NewRequest(&grpcviewv1.CreateScriptRequest{
		WorkspaceName: testWorkspace, Name: "sign", Kind: grpcviewv1.ScriptKind_SCRIPT_KIND_MIDDLEWARE,
	}))
	if err != nil {
		t.Fatalf("CreateScript middleware: %v", err)
	}
	scripts := resp.Msg.GetWorkspace().GetScripts()
	if len(scripts) != 2 || scripts[0].GetName() != "uuid" || scripts[0].GetKind() != grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR {
		t.Fatalf("scripts = %+v, want [uuid(gen) sign(mw)]", scripts)
	}

	// A duplicate name is a FailedPrecondition (store's ErrAlreadyExists).
	if _, err := w.CreateScript(ctx, connect.NewRequest(&grpcviewv1.CreateScriptRequest{
		WorkspaceName: testWorkspace, Name: "uuid", Kind: grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR,
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("duplicate create code = %v, want FailedPrecondition", connect.CodeOf(err))
	}

	// Update source + rename.
	src := `export default () => crypto.randomUUID?.() ?? "x"`
	newName := "uuidv4"
	upd, err := w.UpdateScript(ctx, connect.NewRequest(&grpcviewv1.UpdateScriptRequest{
		WorkspaceName: testWorkspace, Name: "uuid", Source: &src, NewName: &newName,
	}))
	if err != nil {
		t.Fatalf("UpdateScript: %v", err)
	}
	renamed := scriptByName(upd.Msg.GetWorkspace().GetScripts(), "uuidv4")
	if renamed == nil || renamed.GetSource() != src {
		t.Fatalf("rename+source not applied: %+v", upd.Msg.GetWorkspace().GetScripts())
	}

	// Delete.
	del, err := w.DeleteScript(ctx, connect.NewRequest(&grpcviewv1.DeleteScriptRequest{
		WorkspaceName: testWorkspace, Name: "sign",
	}))
	if err != nil {
		t.Fatalf("DeleteScript: %v", err)
	}
	remaining := del.Msg.GetWorkspace().GetScripts()
	if len(remaining) != 1 || remaining[0].GetName() != "uuidv4" {
		t.Fatalf("after delete, scripts = %+v, want [uuidv4]", remaining)
	}
}

func scriptByName(scripts []*grpcviewv1.Script, name string) *grpcviewv1.Script {
	for _, s := range scripts {
		if s.GetName() == name {
			return s
		}
	}
	return nil
}

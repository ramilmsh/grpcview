package workspace

import (
	"context"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func TestResolveInvokeMetadata(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()

	t.Run("evaluates a metadata module to a multi-valued Struct", func(t *testing.T) {
		md, err := w.resolveInvokeMetadata(ctx, testWorkspace, nil,
			`export default () => ({ authorization: ["Bearer " + (1 + 1)], "x-multi": ["a", "b"] })`,
			nil, nil)
		if err != nil {
			t.Fatalf("resolveInvokeMetadata: %v", err)
		}
		if got := md.GetFields()["authorization"].GetListValue().GetValues(); len(got) != 1 || got[0].GetStringValue() != "Bearer 2" {
			t.Fatalf("authorization = %v, want [\"Bearer 2\"]", got)
		}
		multi := md.GetFields()["x-multi"].GetListValue().GetValues()
		if len(multi) != 2 || multi[0].GetStringValue() != "a" || multi[1].GetStringValue() != "b" {
			t.Fatalf("x-multi = %v, want [\"a\" \"b\"]", multi)
		}
	})

	t.Run("empty script returns the fallback Struct verbatim", func(t *testing.T) {
		fallback, _ := structpb.NewStruct(map[string]any{"x-plain": "kept"})
		md, err := w.resolveInvokeMetadata(ctx, testWorkspace, nil, "   ", fallback, nil)
		if err != nil {
			t.Fatalf("resolveInvokeMetadata: %v", err)
		}
		if md != fallback {
			t.Fatalf("empty script should return the fallback Struct pointer unchanged")
		}
	})

	for _, c := range []struct {
		name, script string
		code         connect.Code
	}{
		{"throwing module errors FailedPrecondition", `export default () => { throw new Error("boom") }`, connect.CodeFailedPrecondition},
		{"array return is not an object", `export default () => [1, 2, 3]`, connect.CodeFailedPrecondition},
		{"string return is not an object", `export default () => "nope"`, connect.CodeFailedPrecondition},
		{"number value is not string|string[]", `export default () => ({ "x-n": 7 })`, connect.CodeInvalidArgument},
		{"non-string array element", `export default () => ({ "x-n": ["ok", 7] })`, connect.CodeInvalidArgument},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := w.resolveInvokeMetadata(ctx, testWorkspace, nil, c.script, nil, nil); connect.CodeOf(err) != c.code {
				t.Fatalf("code = %v, want %v (err=%v)", connect.CodeOf(err), c.code, err)
			}
		})
	}
}

func TestResolveInvokeMetadataComposition(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createGenerator(t, w, ctx, "apiToken", `export default () => "tok-42"`)

	md, err := w.resolveInvokeMetadata(ctx, testWorkspace, nil,
		`export default () => ({ authorization: ["Bearer " + apiToken()] })`, nil, nil)
	if err != nil {
		t.Fatalf("resolveInvokeMetadata: %v", err)
	}
	if got := md.GetFields()["authorization"].GetListValue().GetValues(); len(got) != 1 || got[0].GetStringValue() != "Bearer tok-42" {
		t.Fatalf("authorization = %v, want [\"Bearer tok-42\"]", got)
	}
}

func TestInvokeMetadataScript(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createGenerator(t, w, ctx, "bearer", `export default () => "Bearer tok"`)

	port := startEchoServer(t)
	resp, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		Spec: &grpcviewv1.InvokeSpec{
			WorkspaceName:  testWorkspace,
			Service:        echoService,
			Method:         "Unary",
			MetadataScript: `export default () => ({ authorization: [bearer()], "x-scope": ["read", "write"] })`,
			Target:         &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
		Body: `export default () => ({ message: "hi" })`,
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if code := resp.Msg.GetResponse().GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("status = %d (%s)", code, resp.Msg.GetResponse().GetStatus().GetMessage())
	}
	fields := resp.Msg.GetResponse().GetRequestMetadata().GetFields()
	if got := fields["authorization"].GetStringValue(); got != "Bearer tok" {
		t.Fatalf("authorization = %q, want Bearer tok", got)
	}
	scope := fields["x-scope"].GetListValue().GetValues()
	if len(scope) != 2 || scope[0].GetStringValue() != "read" || scope[1].GetStringValue() != "write" {
		t.Fatalf("x-scope = %v, want multi-valued [read write]", scope)
	}
}

package workspace

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

// workspaceAt returns a handler backed by a store rooted at base, so a test can
// reopen the same on-disk collection from a fresh store to prove persistence. It
// carries a real scripting engine because the invoke path now evaluates every body
// as a TypeScript module; the engine is torn down on cleanup.
func workspaceAt(t *testing.T, base string) Workspace {
	t.Helper()
	eng, err := scripting.NewEngine(context.Background(), scriptingMaxPages)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	return Workspace{
		store:  store.New(base, slog.New(slog.NewTextHandler(io.Discard, nil))),
		engine: eng,
	}
}

// loadHistory reopens base with a fresh store and returns the named root
// request's persisted run history — the "reload from disk" the milestone asks to
// verify (no in-memory caches shared with the invoking handler).
func loadHistory(t *testing.T, base, name string) []*grpcviewv1.History {
	t.Helper()
	ctx := context.Background()
	coll, err := store.New(base, slog.New(slog.NewTextHandler(io.Discard, nil))).Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	ws, err := coll.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, it := range ws.GetItem().GetFolder().GetItems() {
		if it.GetName() == name {
			return it.GetRequest().GetHistory()
		}
	}
	t.Fatalf("request %q not found after reload", name)
	return nil
}

// TestInvokeRecordsHistory drives several real unary invokes through the Invoke
// RPC against an in-process echo server and asserts each run is appended (in
// order) to the target request's history and persists across a fresh reload,
// capturing status/latency/timestamp + the request the user sent. An ad-hoc
// invoke (no item_name) records nothing.
func TestInvokeRecordsHistory(t *testing.T) {
	base := t.TempDir()
	w := workspaceAt(t, base)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	// A saved request is the history target; its stored service/method are
	// irrelevant to the call (Target + the fields on InvokeRequest drive that).
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateRequest(ctx, nil, "Echo", echoService, "Unary"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	port := startEchoServer(t)
	target := &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)}

	const runs = 3
	for i := 0; i < runs; i++ {
		md, _ := structpb.NewStruct(map[string]any{"x-run": fmt.Sprintf("%d", i)})
		resp, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
			WorkspaceName: testWorkspace,
			ItemName:      "Echo",
			Service:       echoService,
			Method:        "Unary",
			Body:          tsBody(fmt.Sprintf(`{"message":"hi-%d"}`, i)),
			Metadata:      md,
			Target:        target,
		}))
		if err != nil {
			t.Fatalf("Invoke %d: %v", i, err)
		}
		if got := resp.Msg.GetResponse().GetStatus().GetCode(); got != int32(codeOK) {
			t.Fatalf("Invoke %d status = %d, want OK", i, got)
		}
	}

	// An ad-hoc invoke (empty item_name) must not touch any request's history.
	if _, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		WorkspaceName: testWorkspace,
		Service:       echoService,
		Method:        "Unary",
		Body:          tsBody(`{"message":"ad-hoc"}`),
		Target:        target,
	})); err != nil {
		t.Fatalf("ad-hoc Invoke: %v", err)
	}

	hist := loadHistory(t, base, "Echo")
	if len(hist) != runs {
		t.Fatalf("history len = %d, want %d (ad-hoc invoke must not append)", len(hist), runs)
	}
	for i, h := range hist {
		req, res := h.GetRequest(), h.GetResponse()
		if want := tsBody(fmt.Sprintf(`{"message":"hi-%d"}`, i)); string(req.GetBody()) != want {
			t.Errorf("entry %d body = %s, want %s (append order)", i, req.GetBody(), want)
		}
		if req.GetService() != echoService || req.GetMethod() != "Unary" {
			t.Errorf("entry %d service/method = %q/%q", i, req.GetService(), req.GetMethod())
		}
		if req.GetMetadata().GetFields()["x-run"].GetStringValue() != fmt.Sprintf("%d", i) {
			t.Errorf("entry %d request metadata not captured: %v", i, req.GetMetadata())
		}
		if res.GetStatus().GetCode() != int32(codeOK) {
			t.Errorf("entry %d status = %d, want OK", i, res.GetStatus().GetCode())
		}
		if res.GetLatency() == nil || res.GetTimestamp() == nil {
			t.Errorf("entry %d missing latency/timestamp", i)
		}
		if len(res.GetResponse()) == 0 {
			t.Errorf("entry %d missing response body", i)
		}
	}
}

// TestStreamInvokeRecordsHistory proves the streaming completion point records
// history too — with the terminal status/latency/timestamp but, by design, no
// streamed payloads (the terminal frame's response bytes are empty for streams).
func TestStreamInvokeRecordsHistory(t *testing.T) {
	base := t.TempDir()
	w := workspaceAt(t, base)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateRequest(ctx, nil, "Stream", echoService, "ServerStream"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	port := startEchoServer(t)
	msg := echoStreamReq(port, "ServerStream", `{"message":"hi","count":3}`)
	msg.ItemName = "Stream"
	if _, err := collectStream(ctx, w, msg); err != nil {
		t.Fatalf("streamInvoke: %v", err)
	}

	hist := loadHistory(t, base, "Stream")
	if len(hist) != 1 {
		t.Fatalf("history len = %d, want 1", len(hist))
	}
	res := hist[0].GetResponse()
	if res.GetStatus().GetCode() != int32(codeOK) {
		t.Errorf("status = %d, want OK", res.GetStatus().GetCode())
	}
	if res.GetTimestamp() == nil || res.GetLatency() == nil {
		t.Errorf("missing latency/timestamp")
	}
	// Documented limitation: streamed payloads are NOT recorded (they were the
	// message frames), so the terminal response body stays empty.
	if len(res.GetResponse()) != 0 {
		t.Errorf("streaming history response should be empty, got %d bytes", len(res.GetResponse()))
	}
}

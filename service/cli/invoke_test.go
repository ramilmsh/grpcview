package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

type fakeClient struct {
	snapshot *grpcviewv1.Collection
	getErr   error

	response  *grpcviewv1.Request_Response
	resolved  *grpcviewv1.ResolvedRequest
	frames    []*grpcviewv1.InvokeStreamingResponse
	invokeErr error

	writes writeCalls

	described   *grpcviewv1.DescribeMethodResponse
	describeErr error
	gotDescribe []*grpcviewv1.DescribeMethodRequest

	listing     []*grpcviewv1.CollectionSummary
	listRoot    string
	listTrusted bool
	listErr     error
	gotList     []*grpcviewv1.ListCollectionsRequest

	gotGet         []*grpcviewv1.GetRequest
	gotSaved       []*grpcviewv1.InvokeSavedRequest
	gotSavedStream []*grpcviewv1.InvokeSavedStreamRequest
	gotAdhoc       []*grpcviewv1.InvokeRequest
	gotAdhocStream []*grpcviewv1.InvokeStreamRequest
	closed         int
}

func (f *fakeClient) ServerInfo(_ context.Context, _ *connect.Request[grpcviewv1.ServerInfoRequest]) (*connect.Response[grpcviewv1.ServerInfoResponse], error) {
	return connect.NewResponse(&grpcviewv1.ServerInfoResponse{WorkspaceRoot: f.listRoot}), nil
}

func (f *fakeClient) Shutdown(_ context.Context, _ *connect.Request[grpcviewv1.ShutdownRequest]) (*connect.Response[grpcviewv1.ShutdownResponse], error) {
	return connect.NewResponse(&grpcviewv1.ShutdownResponse{}), nil
}

func (f *fakeClient) Get(_ context.Context, r *connect.Request[grpcviewv1.GetRequest]) (*connect.Response[grpcviewv1.GetResponse], error) {
	f.gotGet = append(f.gotGet, r.Msg)
	if f.getErr != nil {
		return nil, f.getErr
	}
	return connect.NewResponse(&grpcviewv1.GetResponse{Collection: f.snapshot}), nil
}

func (f *fakeClient) ListCollections(_ context.Context, r *connect.Request[grpcviewv1.ListCollectionsRequest]) (*connect.Response[grpcviewv1.ListCollectionsResponse], error) {
	f.gotList = append(f.gotList, r.Msg)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return connect.NewResponse(&grpcviewv1.ListCollectionsResponse{
		Root:        f.listRoot,
		Collections: f.listing,
		Trusted:     f.listTrusted,
	}), nil
}

func (f *fakeClient) Invoke(_ context.Context, r *connect.Request[grpcviewv1.InvokeRequest]) (*connect.Response[grpcviewv1.InvokeResponse], error) {
	f.gotAdhoc = append(f.gotAdhoc, r.Msg)
	if f.invokeErr != nil {
		return nil, f.invokeErr
	}
	return connect.NewResponse(&grpcviewv1.InvokeResponse{Response: f.response}), nil
}

func (f *fakeClient) InvokeSaved(_ context.Context, r *connect.Request[grpcviewv1.InvokeSavedRequest]) (*connect.Response[grpcviewv1.InvokeSavedResponse], error) {
	f.gotSaved = append(f.gotSaved, r.Msg)
	if f.invokeErr != nil {
		return nil, f.invokeErr
	}
	return connect.NewResponse(&grpcviewv1.InvokeSavedResponse{Response: f.response, Resolved: f.resolved}), nil
}

func (f *fakeClient) InvokeStream(_ context.Context, msg *grpcviewv1.InvokeStreamRequest, send func(*grpcviewv1.InvokeStreamingResponse) error) error {
	f.gotAdhocStream = append(f.gotAdhocStream, msg)
	return f.pump(send)
}

func (f *fakeClient) InvokeSavedStream(_ context.Context, msg *grpcviewv1.InvokeSavedStreamRequest, send func(*grpcviewv1.InvokeStreamingResponse) error) error {
	f.gotSavedStream = append(f.gotSavedStream, msg)
	return f.pump(send)
}

func (f *fakeClient) pump(send func(*grpcviewv1.InvokeStreamingResponse) error) error {
	if f.invokeErr != nil {
		return f.invokeErr
	}
	for _, frame := range f.frames {
		if err := send(frame); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeClient) invokeCalls() int {
	return len(f.gotSaved) + len(f.gotSavedStream) + len(f.gotAdhoc) + len(f.gotAdhocStream)
}

func newFake() *fakeClient {
	return &fakeClient{
		snapshot:    testWorkspace(),
		response:    okResponse(`{"token":"t"}`),
		listing:     []*grpcviewv1.CollectionSummary{{Id: ".", Name: "default"}},
		listTrusted: true,
	}
}

func okResponse(body string) *grpcviewv1.Request_Response {
	return &grpcviewv1.Request_Response{
		Status:   &grpcviewv1.Status{},
		Response: []byte(body),
		Latency:  durationpb.New(12 * time.Millisecond),
	}
}

func failedResponse(code int32, message string) *grpcviewv1.Request_Response {
	return &grpcviewv1.Request_Response{
		Status:  &grpcviewv1.Status{Code: code, Message: message},
		Latency: durationpb.New(12 * time.Millisecond),
	}
}

func testWorkspace() *grpcviewv1.Collection {
	return &grpcviewv1.Collection{
		Name: "default",
		Item: testFolder("",
			testFolder("Auth", testRequest("Login", "auth.v1.AuthService", "Login")),
			testRequest("Stream", "echo.v1.EchoService", "ServerStream"),
			testRequest("Upload", "echo.v1.EchoService", "ClientStream"),
			testRequest("Broken", "gone.v1.GoneService", "Missing"),
			testFolder("echo.v1.EchoService", testRequest("Unary", "echo.v1.EchoService", "Unary")),
		),
		Services: []*grpcviewv1.Service{
			{Package: "auth.v1", Name: "AuthService", Methods: []*grpcviewv1.Method{{Name: "Login"}}},
			{Package: "echo.v1", Name: "EchoService", Methods: []*grpcviewv1.Method{
				{Name: "Unary"},
				{Name: "ServerStream", ServerStreaming: true},
				{Name: "ClientStream", ClientStreaming: true},
				{Name: "Bidi", ClientStreaming: true, ServerStreaming: true},
			}},
		},
	}
}

func testFolder(name string, items ...*grpcviewv1.Item) *grpcviewv1.Item {
	return &grpcviewv1.Item{
		Name:    name,
		Content: &grpcviewv1.Item_Folder{Folder: &grpcviewv1.Folder{Items: items}},
	}
}

func testRequest(name, service, method string) *grpcviewv1.Item {
	return &grpcviewv1.Item{
		Name: name,
		Content: &grpcviewv1.Item_Request{Request: &grpcviewv1.Request{
			Name: name, Service: service, Method: method,
		}},
	}
}

func messageFrame(body string) *grpcviewv1.InvokeStreamingResponse {
	return &grpcviewv1.InvokeStreamingResponse{Event: &grpcviewv1.InvokeStreamingResponse_Message{Message: []byte(body)}}
}

func resultFrame(r *grpcviewv1.Request_Response) *grpcviewv1.InvokeStreamingResponse {
	return &grpcviewv1.InvokeStreamingResponse{Event: &grpcviewv1.InvokeStreamingResponse_Result{Result: r}}
}

func runCLI(fc *fakeClient, stdin string, args ...string) (stdout, stderr string, code int) {
	var out, errBuf bytes.Buffer
	s := Streams{In: strings.NewReader(stdin), Out: &out, Err: &errBuf}
	factory := func(context.Context, *globalFlags) (session, error) {
		return session{Client: fc, close: func(context.Context) error { fc.closed++; return nil }}, nil
	}
	code = execute(context.Background(), newRootCmd(s, (&fakeServe{}).serve, factory), args, s)
	return out.String(), errBuf.String(), code
}

func TestInvoke(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		stdin      string
		fake       func(*fakeClient)
		wantOut    string
		wantErr    string
		wantErrHas string
		wantCode   int
		check      func(t *testing.T, fc *fakeClient)
	}{
		{
			name:     "an OK status prints the body on stdout and nothing on stderr",
			args:     []string{"invoke", "Auth/Login"},
			wantOut:  "{\"token\":\"t\"}\n",
			wantCode: 0,
			check: func(t *testing.T, fc *fakeClient) {
				if len(fc.gotSaved) != 1 {
					t.Fatalf("InvokeSaved called %d time(s), want 1", len(fc.gotSaved))
				}
				got := fc.gotSaved[0]
				spec := got.GetSpec()
				if spec.GetCollection() != "." {
					t.Errorf("collection = %q, want %q", spec.GetCollection(), ".")
				}
				if want := []string{"Auth"}; len(spec.GetPath()) != 1 || spec.GetPath()[0] != want[0] {
					t.Errorf("path = %v, want %v", spec.GetPath(), want)
				}
				if spec.GetItemName() != "Login" {
					t.Errorf("item_name = %q, want %q", spec.GetItemName(), "Login")
				}
				if spec.Messages != nil {
					t.Errorf("messages = %v, want nil with no -f and empty stdin", spec.Messages)
				}
				if spec.RecordHistory != nil {
					t.Errorf("record_history = %v, want nil (the server default is true)", spec.GetRecordHistory())
				}
				if got.GetDryRun() {
					t.Error("dry_run must be false without --dry-run")
				}
				if fc.closed != 1 {
					t.Errorf("session closed %d time(s), want 1", fc.closed)
				}
				if len(fc.gotGet) != 1 {
					t.Errorf("Get called %d time(s), want exactly 1 up-front snapshot", len(fc.gotGet))
				}
			},
		},
		{
			name: "a non-OK status is exit 1 in the D9 format with nothing on stdout",
			args: []string{"invoke", "Auth/Login"},
			fake: func(fc *fakeClient) {
				fc.response = failedResponse(5, `tenant "nope" does not exist`)
			},
			wantErr:  "grpcview: Auth/Login: NOT_FOUND: tenant \"nope\" does not exist   (12ms)\n",
			wantCode: 1,
		},
		{
			name: "a Connect error is exit 2",
			args: []string{"invoke", "Auth/Login"},
			fake: func(fc *fakeClient) {
				fc.invokeErr = connect.NewError(connect.CodeUnavailable, errNoTarget)
			},
			wantErrHas: "grpcview: failed to invoke Auth/Login: unavailable: no target configured\n",
			wantCode:   2,
		},
		{
			name:       "a failing Get is exit 2 and invokes nothing",
			args:       []string{"invoke", "Auth/Login"},
			fake:       func(fc *fakeClient) { fc.getErr = connect.NewError(connect.CodeInternal, errNoTarget) },
			wantErrHas: "failed to read collection \".\"",
			wantCode:   2,
			check:      wantNothingInvoked,
		},
		{
			name:       "an ambiguous argument is exit 2 naming both interpretations",
			args:       []string{"invoke", "echo.v1.EchoService/Unary"},
			wantErrHas: "ambiguous argument \"echo.v1.EchoService/Unary\": it names both the saved request echo.v1.EchoService/Unary in collection \"default\" and the method echo.v1.EchoService/Unary in the schema",
			wantCode:   2,
			check:      wantNothingInvoked,
		},
		{
			name:       "an unknown slashed argument is exit 2 saying what was looked for",
			args:       []string{"invoke", "Auth/Nope"},
			wantErrHas: "unknown request or method \"Auth/Nope\": no saved request at that path in collection \"default\", and no service \"Auth\" with a method \"Nope\" among its 2 service(s)",
			wantCode:   2,
			check:      wantNothingInvoked,
		},
		{
			name:       "an unslashed unknown argument says a method argument needs a slash",
			args:       []string{"invoke", "Nope"},
			wantErrHas: "a <service>/<method> argument needs a slash",
			wantCode:   2,
			check:      wantNothingInvoked,
		},
		{
			name:       "a folder is not invokable",
			args:       []string{"invoke", "Auth"},
			wantErrHas: "cannot invoke \"Auth\": it is a folder, not a request",
			wantCode:   2,
			check:      wantNothingInvoked,
		},
		{
			name:       "a saved request whose method is not in the schema is exit 2",
			args:       []string{"invoke", "Broken"},
			wantErrHas: "it calls gone.v1.GoneService/Missing, which no definition source in collection \"default\" resolves",
			wantCode:   2,
			check:      wantNothingInvoked,
		},
		{
			name:     "--param parses JSON and falls back to the literal string",
			args:     []string{"invoke", "Auth/Login", "--param", "n=3", "--param", "who=three", "--param", "on=true"},
			wantOut:  "{\"token\":\"t\"}\n",
			wantCode: 0,
			check: func(t *testing.T, fc *fakeClient) {
				params := fc.gotSaved[0].GetSpec().GetParams().AsMap()
				if got, ok := params["n"].(float64); !ok || got != 3 {
					t.Errorf("params[n] = %#v, want the JSON number 3", params["n"])
				}
				if got, ok := params["who"].(string); !ok || got != "three" {
					t.Errorf("params[who] = %#v, want the string \"three\"", params["who"])
				}
				if got, ok := params["on"].(bool); !ok || !got {
					t.Errorf("params[on] = %#v, want the JSON boolean true", params["on"])
				}
			},
		},
		{
			name:       "--param on the ad-hoc form is exit 2",
			args:       []string{"invoke", "echo.v1.EchoService/Bidi", "--param", "n=3"},
			wantErrHas: "--param does not apply to the ad-hoc method echo.v1.EchoService/Bidi",
			wantCode:   2,
			check:      wantNothingInvoked,
		},
		{
			name:       "--metadata on a saved request is exit 2",
			args:       []string{"invoke", "Auth/Login", "--metadata", "k=v"},
			wantErrHas: "--metadata does not apply to the saved request Auth/Login: its own metadata script owns its metadata",
			wantCode:   2,
			check:      wantNothingInvoked,
		},
		{
			name:       "--dry-run on the ad-hoc form is exit 2",
			args:       []string{"invoke", "auth.v1.AuthService/Login", "--dry-run"},
			wantErrHas: "--dry-run does not apply to the ad-hoc method auth.v1.AuthService/Login",
			wantCode:   2,
			check:      wantNothingInvoked,
		},
		{
			name:       "--tls without --target is exit 2 before anything is opened",
			args:       []string{"invoke", "Auth/Login", "--tls"},
			wantErrHas: "--tls needs --target",
			wantCode:   2,
			check: func(t *testing.T, fc *fakeClient) {
				if len(fc.gotGet) != 0 {
					t.Error("a flag conflict must be caught before the snapshot is read")
				}
			},
		},
		{
			name:       "an unknown -o value is exit 2",
			args:       []string{"invoke", "Auth/Login", "-o", "yaml"},
			wantErrHas: "invalid --output \"yaml\": want one of body, json, raw",
			wantCode:   2,
		},
		{
			name:     "--target and --tls build the Server override",
			args:     []string{"invoke", "Auth/Login", "--target", "127.0.0.1:50055", "--tls"},
			wantOut:  "{\"token\":\"t\"}\n",
			wantCode: 0,
			check: func(t *testing.T, fc *fakeClient) {
				target := fc.gotSaved[0].GetSpec().GetTarget()
				if target.GetAddress() != "127.0.0.1:50055" {
					t.Errorf("target.address = %q, want %q", target.GetAddress(), "127.0.0.1:50055")
				}
				if target.GetTls() == nil {
					t.Error("--tls must set target.tls to the empty TLS message")
				}
			},
		},
		{
			name:     "--no-history sends an explicit false",
			args:     []string{"invoke", "Auth/Login", "--no-history"},
			wantOut:  "{\"token\":\"t\"}\n",
			wantCode: 0,
			check: func(t *testing.T, fc *fakeClient) {
				if spec := fc.gotSaved[0].GetSpec(); spec.RecordHistory == nil || spec.GetRecordHistory() {
					t.Errorf("record_history = %v, want an explicit false", spec.RecordHistory)
				}
			},
		},
		{
			name:     "-o raw writes the response bytes unchanged",
			args:     []string{"invoke", "Auth/Login", "-o", "raw"},
			fake:     func(fc *fakeClient) { fc.response = okResponse("{\n  \"token\": \"t\"\n}") },
			wantOut:  "{\n  \"token\": \"t\"\n}",
			wantCode: 0,
		},
		{
			name:     "-o body flattens a multi-line response to one line",
			args:     []string{"invoke", "Auth/Login"},
			fake:     func(fc *fakeClient) { fc.response = okResponse("{\n  \"token\": \"t\"\n}") },
			wantOut:  "{\"token\":\"t\"}\n",
			wantCode: 0,
		},
		{
			name:     "an ad-hoc unary call sends the piped body verbatim",
			args:     []string{"invoke", "auth.v1.AuthService/Login", "--metadata", "k=v1", "--metadata", "k=v2"},
			stdin:    "{\"user\":\"a\"}\n",
			wantOut:  "{\"token\":\"t\"}\n",
			wantCode: 0,
			check: func(t *testing.T, fc *fakeClient) {
				if len(fc.gotAdhoc) != 1 {
					t.Fatalf("Invoke called %d time(s), want 1", len(fc.gotAdhoc))
				}
				got := fc.gotAdhoc[0]
				spec := got.GetSpec()
				if spec.GetService() != "auth.v1.AuthService" || spec.GetMethod() != "Login" {
					t.Errorf("service/method = %q/%q", spec.GetService(), spec.GetMethod())
				}
				if got.GetBody() != "{\"user\":\"a\"}\n" {
					t.Errorf("body = %q, want the piped bytes unchanged", got.GetBody())
				}
				if len(spec.GetPath()) != 0 || spec.GetItemName() != "" {
					t.Errorf("an ad-hoc call addresses no saved item, got path=%v item=%q", spec.GetPath(), spec.GetItemName())
				}
				values := spec.GetMetadata().GetFields()["k"].GetListValue().GetValues()
				if len(values) != 2 || values[0].GetStringValue() != "v1" || values[1].GetStringValue() != "v2" {
					t.Errorf("metadata[k] = %v, want the list [v1 v2]", values)
				}
			},
		},
		{
			name:     "a multi-line TS module body is one message, never split on newlines",
			args:     []string{"invoke", "Auth/Login", "-f", "-"},
			stdin:    tsBody,
			wantOut:  "{\"token\":\"t\"}\n",
			wantCode: 0,
			check: func(t *testing.T, fc *fakeClient) {
				got := fc.gotSaved[0].GetSpec().GetMessages()
				if len(got) != 1 {
					t.Fatalf("messages = %d entries, want exactly 1: a TS module is one body", len(got))
				}
				if got[0] != tsBody {
					t.Errorf("messages[0] = %q, want the stdin bytes byte-identical (%q)", got[0], tsBody)
				}
			},
		},
		{
			name:     "three NDJSON lines on a client-streaming method become three ordered messages",
			args:     []string{"invoke", "Upload", "-f", "-"},
			stdin:    "{\"i\":1}\n\n{\"i\":2}\n{\"i\":3}\n",
			fake:     func(fc *fakeClient) { fc.frames = []*grpcviewv1.InvokeStreamingResponse{resultFrame(okResponse(""))} },
			wantErr:  "Upload: OK   (12ms)\n",
			wantCode: 0,
			check: func(t *testing.T, fc *fakeClient) {
				if len(fc.gotSavedStream) != 1 {
					t.Fatalf("InvokeSavedStream called %d time(s), want 1: a client-streaming method never goes through the unary RPC", len(fc.gotSavedStream))
				}
				want := []string{"{\"i\":1}", "{\"i\":2}", "{\"i\":3}"}
				got := fc.gotSavedStream[0].GetSpec().GetMessages()
				if len(got) != len(want) {
					t.Fatalf("messages = %v, want %v (blank lines skipped)", got, want)
				}
				for i := range want {
					if got[i] != want[i] {
						t.Errorf("messages[%d] = %q, want %q", i, got[i], want[i])
					}
				}
			},
		},
		{
			name:       "a non-object NDJSON line is exit 2 naming the line number",
			args:       []string{"invoke", "Upload", "-f", "-"},
			stdin:      "{\"i\":1}\n[1,2]\n",
			wantErrHas: "line 2 of the request body is not a JSON object: got an array",
			wantCode:   2,
			check:      wantNothingInvoked,
		},
		{
			name: "a server-streaming saved request streams NDJSON with the terminal frame on stderr",
			args: []string{"invoke", "Stream"},
			fake: func(fc *fakeClient) {
				fc.frames = []*grpcviewv1.InvokeStreamingResponse{
					messageFrame("{\n \"i\": 1\n}"),
					messageFrame(`{"i":2}`),
					resultFrame(okResponse("")),
				}
			},
			wantOut:  "{\"i\":1}\n{\"i\":2}\n",
			wantErr:  "Stream: OK   (12ms)\n",
			wantCode: 0,
			check: func(t *testing.T, fc *fakeClient) {
				if len(fc.gotSavedStream) != 1 || len(fc.gotSaved) != 0 {
					t.Errorf("a server-streaming saved request must use InvokeSavedStream (saved=%d stream=%d)", len(fc.gotSaved), len(fc.gotSavedStream))
				}
			},
		},
		{
			name: "a streaming non-OK terminal frame is exit 1 with the messages still on stdout",
			args: []string{"invoke", "Stream"},
			fake: func(fc *fakeClient) {
				fc.frames = []*grpcviewv1.InvokeStreamingResponse{
					messageFrame(`{"i":1}`),
					resultFrame(failedResponse(13, "boom")),
				}
			},
			wantOut:  "{\"i\":1}\n",
			wantErr:  "grpcview: Stream: INTERNAL: boom   (12ms)\n",
			wantCode: 1,
		},
		{
			name: "a stream that ends without a terminal frame is exit 2",
			args: []string{"invoke", "Stream"},
			fake: func(fc *fakeClient) {
				fc.frames = []*grpcviewv1.InvokeStreamingResponse{messageFrame(`{"i":1}`)}
			},
			wantOut:    "{\"i\":1}\n",
			wantErrHas: "the stream ended without a terminal frame",
			wantCode:   2,
		},
		{
			name:  "an ad-hoc bidi call reads NDJSON and uses the streaming RPC",
			args:  []string{"invoke", "echo.v1.EchoService/Bidi"},
			stdin: "{\"i\":1}\n{\"i\":2}\n",
			fake: func(fc *fakeClient) {
				fc.frames = []*grpcviewv1.InvokeStreamingResponse{resultFrame(okResponse(""))}
			},
			wantErr:  "echo.v1.EchoService/Bidi: OK   (12ms)\n",
			wantCode: 0,
			check: func(t *testing.T, fc *fakeClient) {
				if len(fc.gotAdhocStream) != 1 {
					t.Fatalf("InvokeStream called %d time(s), want 1", len(fc.gotAdhocStream))
				}
				if got := fc.gotAdhocStream[0].GetMessages(); len(got) != 2 {
					t.Errorf("messages = %v, want two", got)
				}
			},
		},
		{
			name:       "invoke needs exactly one argument",
			args:       []string{"invoke"},
			wantErrHas: "accepts 1 arg",
			wantCode:   2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFake()
			if tc.fake != nil {
				tc.fake(fc)
			}

			out, errOut, code := runCLI(fc, tc.stdin, tc.args...)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d (stdout=%q stderr=%q)", code, tc.wantCode, out, errOut)
			}
			if out != tc.wantOut {
				t.Errorf("stdout = %q, want %q", out, tc.wantOut)
			}
			switch {
			case tc.wantErrHas != "":
				if !strings.Contains(errOut, tc.wantErrHas) {
					t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErrHas)
				}
			default:
				if errOut != tc.wantErr {
					t.Errorf("stderr = %q, want %q", errOut, tc.wantErr)
				}
			}
			if tc.check != nil {
				tc.check(t, fc)
			}
		})
	}
}

const tsBody = "export default () => ({\n  user: \"a\",\n  tenant: \"acme\",\n})\n"

func wantNothingInvoked(t *testing.T, fc *fakeClient) {
	t.Helper()
	if fc.invokeCalls() != 0 {
		t.Errorf("%d invoke RPC(s) called, want none", fc.invokeCalls())
	}
}

var errNoTarget = errors.New("no target configured")

func TestInvokeParamsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "params.json")
	if err := os.WriteFile(path, []byte(`{"tenant":"from-file","keep":"yes"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	fc := newFake()
	out, errOut, code := runCLI(fc, "", "invoke", "Auth/Login", "--params-file", path, "--param", "tenant=from-flag")

	if code != 0 || out != "{\"token\":\"t\"}\n" || errOut != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	params := fc.gotSaved[0].GetSpec().GetParams().AsMap()
	if params["tenant"] != "from-flag" {
		t.Errorf("params[tenant] = %#v, want the explicit --param to win", params["tenant"])
	}
	if params["keep"] != "yes" {
		t.Errorf("params[keep] = %#v, want the file's value to survive the merge", params["keep"])
	}
}

func TestInvokeBodyFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.ts")
	if err := os.WriteFile(path, []byte(tsBody), 0o600); err != nil {
		t.Fatal(err)
	}

	fc := newFake()
	_, _, code := runCLI(fc, "", "invoke", "Auth/Login", "-f", path)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := fc.gotSaved[0].GetSpec().GetMessages(); len(got) != 1 || got[0] != tsBody {
		t.Errorf("messages = %q, want one entry equal to the file's bytes", got)
	}
}

func TestInvokeMissingBodyFile(t *testing.T) {
	fc := newFake()
	out, errOut, code := runCLI(fc, "", "invoke", "Auth/Login", "-f", filepath.Join(t.TempDir(), "nope.json"))
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errOut, "failed to read the body file") {
		t.Errorf("stderr = %q, want it to name the failure", errOut)
	}
	wantNothingInvoked(t, fc)
}

func TestInvokeDryRun(t *testing.T) {
	fc := newFake()
	fc.response = nil
	fc.resolved = &grpcviewv1.ResolvedRequest{
		Service:  "auth.v1.AuthService",
		Method:   "Login",
		Target:   &grpcviewv1.Server{Address: "127.0.0.1:50055"},
		Messages: []string{`{"user":"a"}`},
		Metadata: mustStruct(t, map[string]any{"k": []any{"v"}}),
	}

	out, errOut, code := runCLI(fc, "", "invoke", "Auth/Login", "--dry-run")

	if code != 0 {
		t.Errorf("exit code = %d, want 0 (stderr=%q)", code, errOut)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty", errOut)
	}
	if !strings.HasPrefix(out, "{\n  \"") {
		t.Errorf("stdout = %q, want indented JSON", out)
	}
	if !strings.HasSuffix(out, "}\n") {
		t.Errorf("stdout = %q, want one trailing newline", out)
	}
	var resolved map[string]any
	if err := json.Unmarshal([]byte(out), &resolved); err != nil {
		t.Fatalf("stdout is not JSON: %v (%q)", err, out)
	}
	if resolved["service"] != "auth.v1.AuthService" {
		t.Errorf("resolved.service = %#v", resolved["service"])
	}
	if len(fc.gotSaved) != 1 || !fc.gotSaved[0].GetDryRun() {
		t.Fatalf("want exactly one InvokeSaved with dry_run set, got %d call(s)", len(fc.gotSaved))
	}
	if len(fc.gotSavedStream)+len(fc.gotAdhoc)+len(fc.gotAdhocStream) != 0 {
		t.Error("a dry run must call no RPC other than InvokeSaved with dry_run")
	}
}

func TestInvokeDryRunOnAStreamingMethod(t *testing.T) {
	fc := newFake()
	fc.resolved = &grpcviewv1.ResolvedRequest{Service: "echo.v1.EchoService", Method: "ServerStream"}

	_, errOut, code := runCLI(fc, "", "invoke", "Stream", "--dry-run")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%q)", code, errOut)
	}
	if len(fc.gotSaved) != 1 || len(fc.gotSavedStream) != 0 {
		t.Errorf("dry run used saved=%d stream=%d, want the unary RPC", len(fc.gotSaved), len(fc.gotSavedStream))
	}
}

func TestInvokeOutputJSON(t *testing.T) {
	fc := newFake()
	out, errOut, code := runCLI(fc, "", "invoke", "Auth/Login", "-o", "json")

	if code != 0 || errOut != "" {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Errorf("stdout = %q, want exactly one line", out)
	}
	var frame struct {
		Response string `json:"response"`
		Latency  string `json:"latency"`
	}
	if err := json.Unmarshal([]byte(out), &frame); err != nil {
		t.Fatalf("stdout is not JSON: %v (%q)", err, out)
	}
	body, err := base64.StdEncoding.DecodeString(frame.Response)
	if err != nil {
		t.Fatalf("response is not base64 protojson bytes: %v", err)
	}
	if string(body) != `{"token":"t"}` {
		t.Errorf("decoded response = %q, want the response body", body)
	}
	if frame.Latency == "" {
		t.Error("want the latency in -o json output")
	}
}

func TestInvokeOutputJSONOnFailure(t *testing.T) {
	fc := newFake()
	fc.response = failedResponse(5, `tenant "nope" does not exist`)

	out, errOut, code := runCLI(fc, "", "invoke", "Auth/Login", "-o", "json")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stdout=%q stderr=%q)", code, out, errOut)
	}
	var frame struct {
		Status struct {
			Code int32 `json:"code"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &frame); err != nil {
		t.Fatalf("stdout is not JSON: %v (%q)", err, out)
	}
	if frame.Status.Code != 5 {
		t.Errorf("status.code = %d, want 5: the failure status is the data -o json exists to carry", frame.Status.Code)
	}
	if !strings.Contains(errOut, "NOT_FOUND") {
		t.Errorf("stderr = %q, want the one-line NOT_FOUND diagnostic", errOut)
	}

	fc2 := newFake()
	fc2.response = failedResponse(5, "nope")
	body, _, code2 := runCLI(fc2, "", "invoke", "Auth/Login")
	if code2 != 1 || body != "" {
		t.Errorf("-o body on failure: code=%d stdout=%q, want 1 and empty", code2, body)
	}
}

func TestInvokeStreamOutputJSON(t *testing.T) {
	fc := newFake()
	fc.frames = []*grpcviewv1.InvokeStreamingResponse{
		messageFrame(`{"i":1}`),
		resultFrame(okResponse("")),
	}

	out, errOut, code := runCLI(fc, "", "invoke", "Stream", "-o", "json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%q)", code, errOut)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty: -o json puts the terminal frame on stdout", errOut)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout = %q, want two lines", out)
	}
	if lines[0] != `{"i":1}` {
		t.Errorf("first line = %q, want the message", lines[0])
	}
	if !strings.Contains(lines[1], "latency") {
		t.Errorf("last line = %q, want the terminal frame", lines[1])
	}
}

func TestInvokeAppliesTheGlobalTimeout(t *testing.T) {
	var out, errBuf bytes.Buffer
	s := Streams{In: strings.NewReader(""), Out: &out, Err: &errBuf}
	fc := newFake()

	var deadline time.Time
	factory := func(ctx context.Context, g *globalFlags) (session, error) {
		deadline, _ = ctx.Deadline()
		if g.Collection != "other" {
			t.Errorf("--collection reached the factory as %q, want %q", g.Collection, "other")
		}
		return session{Client: fc, close: func(context.Context) error { return nil }}, nil
	}

	code := execute(context.Background(), newRootCmd(s, (&fakeServe{}).serve, factory),
		[]string{"invoke", "Auth/Login", "--timeout", "5s", "--collection", "other"}, s)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%q)", code, errBuf.String())
	}
	if deadline.IsZero() {
		t.Error("want --timeout applied as a context deadline around the call")
	}
	if fc.gotSaved[0].GetSpec().GetCollection() != "other" {
		t.Errorf("collection = %q, want %q", fc.gotSaved[0].GetSpec().GetCollection(), "other")
	}
}

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The RPCs no verb calls. Stubbed rather than implemented so a verb that starts calling one
// fails loudly here instead of silently passing against an empty response.

func (f *fakeClient) UpdateCollection(context.Context, *connect.Request[grpcviewv1.UpdateCollectionRequest]) (*connect.Response[grpcviewv1.UpdateCollectionResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("fakeClient: UpdateCollection is not exercised by any verb"))
}

func (f *fakeClient) ListBazelTargets(context.Context, *connect.Request[grpcviewv1.ListBazelTargetsRequest]) (*connect.Response[grpcviewv1.ListBazelTargetsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("fakeClient: ListBazelTargets is not exercised by any verb"))
}

func (f *fakeClient) UpdateFolder(context.Context, *connect.Request[grpcviewv1.UpdateFolderRequest]) (*connect.Response[grpcviewv1.UpdateFolderResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("fakeClient: UpdateFolder is not exercised by any verb"))
}

func (f *fakeClient) CreateScript(context.Context, *connect.Request[grpcviewv1.CreateScriptRequest]) (*connect.Response[grpcviewv1.CreateScriptResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("fakeClient: CreateScript is not exercised by any verb"))
}

func (f *fakeClient) UpdateScript(context.Context, *connect.Request[grpcviewv1.UpdateScriptRequest]) (*connect.Response[grpcviewv1.UpdateScriptResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("fakeClient: UpdateScript is not exercised by any verb"))
}

func (f *fakeClient) DeleteScript(context.Context, *connect.Request[grpcviewv1.DeleteScriptRequest]) (*connect.Response[grpcviewv1.DeleteScriptResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("fakeClient: DeleteScript is not exercised by any verb"))
}

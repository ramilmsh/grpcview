package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func lsWorkspace() *grpcviewv1.Workspace {
	return &grpcviewv1.Workspace{
		Name: "default",
		Item: testFolder("",
			testFolder("Auth",
				testRequest("Login", "auth.v1.AuthService", "Login"),
				testFolder("Admin",
					withMiddleware(testRequest("Grant", "auth.v1.AuthService", "Grant"), "sign", "trace")),
			),
			testRequest("Ping", "echo.v1.EchoService", "Unary"),
		),
	}
}

func withMiddleware(item *grpcviewv1.Item, names ...string) *grpcviewv1.Item {
	item.GetRequest().Middleware = names
	return item
}

const (
	lsGoldenRoot = `Auth/             folder
Auth/Login        auth.v1.AuthService/Login
Auth/Admin/       folder
Auth/Admin/Grant  auth.v1.AuthService/Grant  [2 middleware]
Ping              echo.v1.EchoService/Unary
`
	lsGoldenSubtree = `Auth/Login        auth.v1.AuthService/Login
Auth/Admin/       folder
Auth/Admin/Grant  auth.v1.AuthService/Grant  [2 middleware]
`
)

func TestLs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		fake       func(*fakeClient)
		wantOut    string
		wantErrHas string
		wantCode   int
	}{
		{
			name:     "the whole collection, depth first, in stored order",
			args:     []string{"ls"},
			wantOut:  lsGoldenRoot,
			wantCode: 0,
		},
		{
			name:     "a subtree, still fully qualified so it stays invoke-pasteable",
			args:     []string{"ls", "Auth"},
			wantOut:  lsGoldenSubtree,
			wantCode: 0,
		},
		{
			name:     "the trailing slash ls itself prints is accepted back",
			args:     []string{"ls", "Auth/"},
			wantOut:  lsGoldenSubtree,
			wantCode: 0,
		},
		{
			name:     "a nested folder path",
			args:     []string{"ls", "Auth/Admin"},
			wantOut:  "Auth/Admin/Grant  auth.v1.AuthService/Grant  [2 middleware]\n",
			wantCode: 0,
		},
		{
			name:       "an unknown folder is grpcview's own failure",
			args:       []string{"ls", "Nope"},
			wantErrHas: `grpcview: unknown folder "Nope": no folder at that path in workspace "default"`,
			wantCode:   2,
		},
		{
			name:       "an unknown nested folder",
			args:       []string{"ls", "Auth/Nope"},
			wantErrHas: `unknown folder "Auth/Nope"`,
			wantCode:   2,
		},
		{
			name:       "a request path is not a folder",
			args:       []string{"ls", "Auth/Login"},
			wantErrHas: `cannot list "Auth/Login": it is a request, not a folder`,
			wantCode:   2,
		},
		{
			name:       "an invalid -o value never reaches the workspace",
			args:       []string{"ls", "-o", "yaml"},
			wantErrHas: `invalid --output "yaml": want one of text, json`,
			wantCode:   2,
		},
		{
			name:       "a Connect error from Get is exit 2, never exit 1",
			args:       []string{"ls"},
			fake:       func(fc *fakeClient) { fc.getErr = connect.NewError(connect.CodeUnavailable, errNoTarget) },
			wantErrHas: `failed to read workspace "default"`,
			wantCode:   2,
		},
		{
			name:     "an empty collection lists nothing, successfully",
			args:     []string{"ls"},
			fake:     func(fc *fakeClient) { fc.snapshot = &grpcviewv1.Workspace{Name: "default", Item: testFolder("")} },
			wantOut:  "",
			wantCode: 0,
		},
		{
			name: "a request with no method picked yet still lists",
			args: []string{"ls"},
			fake: func(fc *fakeClient) {
				fc.snapshot = &grpcviewv1.Workspace{Name: "default", Item: testFolder("", testRequest("Draft", "", ""))}
			},
			wantOut:  "Draft  -\n",
			wantCode: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFake()
			fc.snapshot = lsWorkspace()
			if tc.fake != nil {
				tc.fake(fc)
			}

			out, errOut, code := runCLI(fc, "", tc.args...)

			if out != tc.wantOut {
				t.Errorf("stdout:\n%q\nwant:\n%q", out, tc.wantOut)
			}
			if tc.wantErrHas == "" {
				if errOut != "" {
					t.Errorf("stderr = %q, want empty", errOut)
				}
			} else if !strings.Contains(errOut, tc.wantErrHas) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErrHas)
			}
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", code, tc.wantCode)
			}
			if fc.invokeCalls() != 0 {
				t.Errorf("ls invoked %d call(s); it must invoke nothing", fc.invokeCalls())
			}
		})
	}
}

func TestLsOutputJSON(t *testing.T) {
	fc := newFake()
	fc.snapshot = lsWorkspace()

	out, errOut, code := runCLI(fc, "", "ls", "Auth", "-o", "json")

	if code != 0 || errOut != "" {
		t.Fatalf("exit code = %d, stderr = %q; want 0 and empty", code, errOut)
	}
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Errorf("stdout is not exactly one line: %q", out)
	}

	var item struct {
		Name   string `json:"name"`
		Folder struct {
			Items []struct {
				Name    string `json:"name"`
				Request struct {
					Service string `json:"service"`
					Method  string `json:"method"`
				} `json:"request"`
			} `json:"items"`
		} `json:"folder"`
	}
	if err := json.Unmarshal([]byte(out), &item); err != nil {
		t.Fatalf("stdout does not parse as JSON: %v\n%s", err, out)
	}
	if item.Name != "Auth" {
		t.Errorf("name = %q, want the folder the argument named", item.Name)
	}
	if len(item.Folder.Items) != 2 {
		t.Fatalf("items = %d, want the folder's 2 children", len(item.Folder.Items))
	}
	if got := item.Folder.Items[0].Request.Service; got != "auth.v1.AuthService" {
		t.Errorf("first child's service = %q, want protojson's own field names", got)
	}
}

func TestLsRootJSON(t *testing.T) {
	fc := newFake()
	fc.snapshot = lsWorkspace()

	out, errOut, code := runCLI(fc, "", "ls", "-o", "json")

	if code != 0 || errOut != "" {
		t.Fatalf("exit code = %d, stderr = %q; want 0 and empty", code, errOut)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("stdout does not parse as JSON: %v\n%s", err, out)
	}
	if _, ok := root["folder"]; !ok {
		t.Errorf("root JSON has no folder: %s", out)
	}
}

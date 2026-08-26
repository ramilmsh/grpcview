package cli

import (
	"strings"
	"testing"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func TestResolveInvokeArg(t *testing.T) {
	ws := testWorkspace()

	for _, tc := range []struct {
		name    string
		arg     string
		want    invokeTarget
		wantErr string
	}{
		{
			name: "a nested saved request",
			arg:  "Auth/Login",
			want: invokeTarget{arg: "Auth/Login", saved: true, parent: []string{"Auth"}, itemName: "Login", service: "auth.v1.AuthService", method: "Login"},
		},
		{
			name: "a top-level saved request keeps an empty parent path",
			arg:  "Stream",
			want: invokeTarget{arg: "Stream", saved: true, parent: []string{}, itemName: "Stream", service: "grpcview.echo.v1.EchoService", method: "ServerStream", kind: methodKind{server: true}},
		},
		{
			name: "a saved client-streaming request",
			arg:  "Upload",
			want: invokeTarget{arg: "Upload", saved: true, parent: []string{}, itemName: "Upload", service: "grpcview.echo.v1.EchoService", method: "ClientStream", kind: methodKind{client: true}},
		},
		{
			name: "an ad-hoc method",
			arg:  "auth.v1.AuthService/Login",
			want: invokeTarget{arg: "auth.v1.AuthService/Login", service: "auth.v1.AuthService", method: "Login"},
		},
		{
			name: "an ad-hoc bidi method",
			arg:  "grpcview.echo.v1.EchoService/Bidi",
			want: invokeTarget{arg: "grpcview.echo.v1.EchoService/Bidi", service: "grpcview.echo.v1.EchoService", method: "Bidi", kind: methodKind{client: true, server: true}},
		},
		{name: "both forms matching is refused", arg: "grpcview.echo.v1.EchoService/Unary", wantErr: "ambiguous argument"},
		{name: "a folder is not a request", arg: "Auth", wantErr: "it is a folder, not a request"},
		{name: "an unknown method on a known service", arg: "auth.v1.AuthService/Nope", wantErr: "unknown request or method"},
		{name: "an unresolvable saved method", arg: "Broken", wantErr: "which no definition source in collection"},
		{name: "a trailing slash is not a path", arg: "Auth/", wantErr: "unknown request or method"},
		{name: "the empty argument", arg: "", wantErr: "unknown request"},
		{name: "a path through a request rather than a folder", arg: "Stream/Nested", wantErr: "unknown request or method"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveInvokeArg(ws, tc.arg)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("resolveInvokeArg(%q) = %+v, want an error", tc.arg, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveInvokeArg(%q): %v", tc.arg, err)
			}
			if got.saved != tc.want.saved || got.itemName != tc.want.itemName ||
				got.service != tc.want.service || got.method != tc.want.method || got.kind != tc.want.kind {
				t.Errorf("resolveInvokeArg(%q) = %+v, want %+v", tc.arg, got, tc.want)
			}
			if strings.Join(got.parent, "/") != strings.Join(tc.want.parent, "/") {
				t.Errorf("parent = %v, want %v", got.parent, tc.want.parent)
			}
		})
	}
}

func TestServiceFullName(t *testing.T) {
	if got := serviceFullName(&grpcviewv1.Service{Package: "a.v1", Name: "S"}); got != "a.v1.S" {
		t.Errorf("serviceFullName = %q, want %q", got, "a.v1.S")
	}
	if got := serviceFullName(&grpcviewv1.Service{Name: "S"}); got != "S" {
		t.Errorf("serviceFullName = %q, want %q", got, "S")
	}
}

func TestMethodKindRouting(t *testing.T) {
	for _, tc := range []struct {
		kind          methodKind
		streaming     bool
		readsAsNDJSON bool
	}{
		{kind: methodKind{}, streaming: false, readsAsNDJSON: false},
		{kind: methodKind{server: true}, streaming: true, readsAsNDJSON: false},
		{kind: methodKind{client: true}, streaming: true, readsAsNDJSON: true},
		{kind: methodKind{client: true, server: true}, streaming: true, readsAsNDJSON: true},
	} {
		if got := tc.kind.streaming(); got != tc.streaming {
			t.Errorf("%+v.streaming() = %v, want %v", tc.kind, got, tc.streaming)
		}
		if got := tc.kind.ndjson(); got != tc.readsAsNDJSON {
			t.Errorf("%+v.ndjson() = %v, want %v", tc.kind, got, tc.readsAsNDJSON)
		}
	}
}

func TestStatusCodeName(t *testing.T) {
	for code, want := range map[int32]string{
		0:  "OK",
		5:  "NOT_FOUND",
		7:  "PERMISSION_DENIED",
		13: "INTERNAL",
		16: "UNAUTHENTICATED",
		99: "CODE_99",
	} {
		if got := statusCodeName(code); got != want {
			t.Errorf("statusCodeName(%d) = %q, want %q", code, got, want)
		}
	}
}

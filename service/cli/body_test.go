package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadBody(t *testing.T) {
	file := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(file, []byte(`{"from":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		file  string
		stdin string
		want  string
	}{
		{name: "no -f reads piped stdin", stdin: `{"a":1}`, want: `{"a":1}`},
		{name: "-f - reads stdin", file: "-", stdin: `{"a":1}`, want: `{"a":1}`},
		{name: "-f reads the named file", file: file, want: `{"from":"file"}`},
		{name: "empty stdin is no body at all", stdin: "", want: ""},
		{name: "whitespace-only stdin is no body at all", stdin: "  \n\t\n", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readBody(Streams{In: strings.NewReader(tc.stdin), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}, tc.file)
			if err != nil {
				t.Fatalf("readBody: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("readBody = %q, want %q", got, tc.want)
			}
			if tc.want == "" && got != nil {
				t.Errorf("readBody = %q, want a nil slice for 'no body'", got)
			}
		})
	}
}

func TestIsPiped(t *testing.T) {
	if !isPiped(&bytes.Buffer{}) {
		t.Error("a bytes.Buffer must count as piped so tests can feed stdin")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if !isPiped(r) {
		t.Error("an os.Pipe read end must count as piped")
	}

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("no %s to stand in for a terminal: %v", os.DevNull, err)
	}
	defer devNull.Close()
	if isPiped(devNull) {
		t.Errorf("a character device (%s) must not count as piped", os.DevNull)
	}
}

func TestBodyMessages(t *testing.T) {
	const multiline = "export default () => ({\n  a: 1,\n})\n"

	for _, tc := range []struct {
		name string
		raw  string
		kind methodKind
		want []string
	}{
		{name: "no input is no messages", raw: "", kind: methodKind{}, want: nil},
		{
			name: "a unary body is one message verbatim",
			raw:  `{"a":1}`,
			kind: methodKind{},
			want: []string{`{"a":1}`},
		},
		{
			name: "a multi-line body on a non-client-streaming method is NOT split",
			raw:  multiline,
			kind: methodKind{},
			want: []string{multiline},
		},
		{
			name: "a server-streaming method still takes one message",
			raw:  multiline,
			kind: methodKind{server: true},
			want: []string{multiline},
		},
		{
			name: "a client-streaming method reads NDJSON, skipping blank lines",
			raw:  "{\"i\":1}\n\n  \n{\"i\":2}\n",
			kind: methodKind{client: true},
			want: []string{`{"i":1}`, `{"i":2}`},
		},
		{
			name: "a bidi method reads NDJSON too",
			raw:  "{\"i\":1}\n{\"i\":2}",
			kind: methodKind{client: true, server: true},
			want: []string{`{"i":1}`, `{"i":2}`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bodyMessages([]byte(tc.raw), tc.kind)
			if err != nil {
				t.Fatalf("bodyMessages: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("bodyMessages = %q, want %q", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("messages[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestBodyMessagesNDJSONErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "an array line", raw: "{\"i\":1}\n[1,2]\n", want: "line 2 of the request body is not a JSON object: got an array"},
		{name: "a scalar line", raw: "42\n", want: "line 1 of the request body is not a JSON object: got a number"},
		{name: "a blank line does not shift the number", raw: "\n\nnot json\n", want: "line 3 of the request body is not a JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := bodyMessages([]byte(tc.raw), methodKind{client: true})
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

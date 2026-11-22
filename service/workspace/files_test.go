package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func newFilesWorkspace(t *testing.T, root string) Workspace {
	t.Helper()
	st := store.New(root, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	w := Workspace{store: st, defs: newDefinitionsCache(), files: newFileMounts(st)}
	t.Cleanup(w.files.close)
	return w
}

// exampleFixture mirrors example/'s node classes (#87 D1): a collection with a folder, a request, both
// item scripts, a script, an upload source, a workspace module, and hidden entries beside them.
func exampleFixture(t *testing.T) (Workspace, string) {
	t.Helper()
	root := t.TempDir()
	w := newFilesWorkspace(t, root)
	ctx := context.Background()
	coll, err := w.store.Open(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	folderMD, body, reqMD, script := "export default {};\n", tsBody("{}")+"\n", "export default { a: '1' };\n", "export const auth = 1;\n"
	for _, step := range []error{
		coll.Create(ctx, "example"),
		coll.CreateFolder(ctx, nil, "users"),
		coll.UpdateFolder(ctx, nil, "users", store.FolderPatch{DraftMetadataScript: &folderMD}),
		coll.CreateRequest(ctx, []string{"users"}, "get", "", ""),
		coll.UpdateRequest(ctx, []string{"users"}, "get", store.RequestPatch{DraftBody: &body, DraftMetadataScript: &reqMD}),
		coll.CreateScript(ctx, "scripts/auth.ts"),
		coll.UpdateScript(ctx, "scripts/auth.ts", store.ScriptPatch{Source: &script}),
	} {
		if step != nil {
			t.Fatal(step)
		}
	}
	uploadTo(t, w, ctx, "example", fileDescriptorSet(t, "grpcview/echo/v1/echo.proto"))
	writeModuleFile(t, root, "libs/common.ts", "export const common = 1;\n")
	writeModuleFile(t, root, ".env", "SECRET=1\n")
	writeModuleFile(t, root, "example/.npmrc", "token\n")
	writeModuleFile(t, root, "node_modules/dep/index.ts", "x\n")
	return w, root
}

func fileErrorOf(t *testing.T, err error) *grpcviewv1.FileError {
	t.Helper()
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a connect error", err)
	}
	for _, d := range ce.Details() {
		if v, derr := d.Value(); derr == nil {
			if fe, ok := v.(*grpcviewv1.FileError); ok {
				return fe
			}
		}
	}
	t.Fatalf("err = %v carries no FileError", err)
	return nil
}

func readFile(t *testing.T, w Workspace, p string) *grpcviewv1.ReadFileResponse {
	t.Helper()
	resp, err := w.ReadFile(context.Background(), connect.NewRequest(&grpcviewv1.ReadFileRequest{Path: p}))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", p, err)
	}
	return resp.Msg
}

func readDir(t *testing.T, w Workspace, p string, recursive bool) map[string]grpcviewv1.FileType {
	t.Helper()
	resp, err := w.ReadDirectory(context.Background(), connect.NewRequest(&grpcviewv1.ReadDirectoryRequest{Path: p, Recursive: recursive}))
	if err != nil {
		t.Fatalf("ReadDirectory(%s): %v", p, err)
	}
	out := map[string]grpcviewv1.FileType{}
	for _, e := range resp.Msg.GetEntries() {
		out[e.GetName()] = e.GetType()
	}
	return out
}

// wantErrno asserts every read of p fails with code, the detail naming p and no host path.
func wantErrno(t *testing.T, w Workspace, root, p string, code grpcviewv1.FileError_Code) {
	t.Helper()
	ctx := context.Background()
	_, statErr := w.Stat(ctx, connect.NewRequest(&grpcviewv1.StatRequest{Path: p}))
	_, readErr := w.ReadFile(ctx, connect.NewRequest(&grpcviewv1.ReadFileRequest{Path: p}))
	for _, err := range []error{statErr, readErr} {
		if err == nil {
			t.Errorf("%s: no error, want %v", p, code)
			continue
		}
		fe := fileErrorOf(t, err)
		if fe.GetCode() != code || fe.GetPath() != p {
			t.Errorf("%s: FileError = %v, want %v", p, fe, code)
		}
		if strings.Contains(err.Error(), root) || strings.Contains(fe.GetMessage(), root) {
			t.Errorf("%s: error %q names a host path", p, err)
		}
	}
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestFilesReadEveryNodeClass(t *testing.T) {
	w, root := exampleFixture(t)
	ctx := context.Background()

	for _, p := range []string{
		"/example/grpcview.json",
		"/example/tree/users/folder.json",
		"/example/tree/users/metadata.ts",
		"/example/tree/users/get/request.json",
		"/example/tree/users/get/body.ts",
		"/example/tree/users/get/metadata.ts",
		"/example/scripts/auth.ts",
		"/libs/common.ts",
	} {
		disk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		got := readFile(t, w, p)
		if got.GetContent() != string(disk) || got.GetStat().GetVersion() != sha(string(disk)) ||
			got.GetStat().GetSize() != int64(len(disk)) || got.GetStat().GetType() != regularFile || got.GetStat().GetMtimeNs() == 0 {
			t.Errorf("%s: %v, want disk content and its sha256", p, got)
		}
	}

	proto := readFile(t, w, "/example/.grpcview/proto/grpcview/echo/v1/echo.proto")
	if !strings.Contains(proto.GetContent(), "service Echo") || proto.GetStat().GetLabel() != "upload:"+testUploadName ||
		proto.GetStat().GetVersion() != sha(proto.GetContent()) {
		t.Errorf("proto mount file = %v", proto)
	}

	var modules struct {
		Paths  []string `json:"paths"`
		Digest string   `json:"digest"`
	}
	raw := readFile(t, w, "/.grpcview/modules.json")
	if err := json.Unmarshal([]byte(raw.GetContent()), &modules); err != nil {
		t.Fatal(err)
	}
	if want := []string{"/example/scripts/auth.ts", "/libs/common.ts"}; !slices.Equal(modules.Paths, want) || len(modules.Digest) != 64 {
		t.Errorf("modules.json = %+v, want paths %v and a digest", modules, want)
	}

	if got := readDir(t, w, "/example/.grpcview/types", false); len(got) != 0 {
		t.Errorf("types mount = %v, want empty", got)
	}
	for _, p := range []string{"/", "/example", "/example/.grpcview", "/example/.grpcview/proto/grpcview", "/example/.grpcview/types", "/.grpcview"} {
		resp, err := w.Stat(ctx, connect.NewRequest(&grpcviewv1.StatRequest{Path: p}))
		if err != nil || resp.Msg.GetStat().GetType() != dirFile || resp.Msg.GetStat().GetVersion() != "" {
			t.Errorf("Stat(%s) = %v, %v; want a directory", p, resp, err)
		}
	}

	all := readDir(t, w, "/", true)
	for name, typ := range map[string]grpcviewv1.FileType{
		".grpcview":                                           dirFile,
		".grpcview/modules.json":                              regularFile,
		"example/grpcview.json":                               regularFile,
		"example/tree/users/get/body.ts":                      regularFile,
		"example/tree/users/metadata.ts":                      regularFile,
		"example/scripts/auth.ts":                             regularFile,
		"example/.grpcview/proto/grpcview/echo/v1":            dirFile,
		"example/.grpcview/proto/grpcview/echo/v1/echo.proto": regularFile,
		"example/.grpcview/types":                             dirFile,
		"libs/common.ts":                                      regularFile,
	} {
		if all[name] != typ {
			t.Errorf("recursive listing: %s = %v, want %v", name, all[name], typ)
		}
	}
	for name := range all {
		if strings.Contains(name, ".env") || strings.Contains(name, ".npmrc") || strings.Contains(name, "node_modules") {
			t.Errorf("recursive listing has hidden %s", name)
		}
	}
	if got := readDir(t, w, "/example/tree/users", false); len(got) != 3 || got["get"] != dirFile || got["folder.json"] != regularFile {
		t.Errorf("ReadDirectory(/example/tree/users) = %v", got)
	}
}

func TestFilesProtoMountIsProtoFiles(t *testing.T) {
	w, _ := exampleFixture(t)
	ctx := context.Background()
	coll, err := w.store.Open(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	sources, err := coll.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defs, err := w.definitionsFor(ctx, coll, sources)
	if err != nil {
		t.Fatal(err)
	}
	want, err := defs.protoFiles()
	if err != nil || len(want) == 0 {
		t.Fatalf("protoFiles = %v, %v", want, err)
	}
	listed := readDir(t, w, "/example/.grpcview/proto", true)
	var files []string
	for name, typ := range listed {
		if typ == regularFile {
			files = append(files, name)
		}
	}
	slices.Sort(files)
	var wantPaths []string
	for _, f := range want {
		wantPaths = append(wantPaths, f.GetPath())
		got := readFile(t, w, "/example/.grpcview/proto/"+f.GetPath())
		if got.GetContent() != f.GetContent() || got.GetStat().GetLabel() != f.GetSource() {
			t.Errorf("%s differs from protoFiles()", f.GetPath())
		}
	}
	if !slices.Equal(files, wantPaths) {
		t.Errorf("proto mount files = %v, want %v", files, wantPaths)
	}
}

func TestFilesEscapesAreEACCES(t *testing.T) {
	w, root := exampleFixture(t)
	outside := t.TempDir()
	writeModuleFile(t, outside, "secret.ts", "x\n")
	for link, target := range map[string]string{
		"escape.ts":              filepath.Join(outside, "secret.ts"),
		"escdir":                 outside,
		"example/scripts/out.ts": filepath.Join(outside, "secret.ts"),
	} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	for _, p := range []string{"/../secret.ts", "/example/../../secret.ts", "/escape.ts", "/escdir/secret.ts", "/example/scripts/out.ts", "relative.ts"} {
		wantErrno(t, w, root, p, grpcviewv1.FileError_CODE_EACCES)
	}
	_, err := w.ReadDirectory(context.Background(), connect.NewRequest(&grpcviewv1.ReadDirectoryRequest{Path: "/escdir"}))
	if fe := fileErrorOf(t, err); fe.GetCode() != grpcviewv1.FileError_CODE_EACCES {
		t.Errorf("ReadDirectory(/escdir) = %v, want EACCES", fe)
	}
	if got := readDir(t, w, "/", false); got["escape.ts"] != 0 || got["escdir"] != 0 {
		t.Errorf("listing has escaping links: %v", got)
	}
}

func TestFilesHiddenAndUnmountedAreENOENT(t *testing.T) {
	w, root := exampleFixture(t)
	// Disk beneath a mount, and .grpcview where no mount is, are never read.
	writeModuleFile(t, root, "example/.grpcview/proto/disk.proto", "x\n")
	writeModuleFile(t, root, "example/.grpcview/types/digest", "x\n")
	writeModuleFile(t, root, "example/tree/.grpcview/x.ts", "x\n")
	writeModuleFile(t, root, "other/.grpcview/x.ts", "x\n")
	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, "env.ts")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, p := range []string{
		"/.env", "/example/.npmrc", "/node_modules/dep/index.ts", "/env.ts", "/missing.ts",
		"/example/.grpcview/proto/disk.proto", "/example/.grpcview/types/digest", "/example/.grpcview/other",
		"/example/.grpcview/modules.json", "/example/tree/.grpcview/x.ts", "/other/.grpcview/x.ts",
		"/.grpcview/proto", "/.grpcview/types",
	} {
		wantErrno(t, w, root, p, grpcviewv1.FileError_CODE_ENOENT)
	}
	if got := readDir(t, w, "/", false); got[".env"] != 0 || got["env.ts"] != 0 || got["node_modules"] != 0 {
		t.Errorf("listing has hidden entries: %v", got)
	}
	if got := readDir(t, w, "/example/.grpcview/proto", false); got["disk.proto"] != 0 {
		t.Errorf("proto mount lists disk: %v", got)
	}
	if got := readDir(t, w, "/.grpcview", false); len(got) != 1 || got["modules.json"] != regularFile {
		t.Errorf("/.grpcview = %v, want only modules.json", got)
	}
}

func TestFilesStateRootIsUnreachable(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	st := store.New(root, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w := Workspace{store: st, defs: newDefinitionsCache()}
	writeModuleFile(t, root, "state/history.json", "{}")
	wantErrno(t, w, root, "/state/history.json", grpcviewv1.FileError_CODE_ENOENT)
	if got := readDir(t, w, "/", false); got["state"] != 0 {
		t.Errorf("listing has the state root: %v", got)
	}
}

func TestFilesReadFilesPerPathErrors(t *testing.T) {
	w, _ := exampleFixture(t)
	paths := []string{"/example/scripts/auth.ts", "/missing.ts", "/example", "/../x", "/example/.grpcview/proto/grpcview/echo/v1/echo.proto", "/.grpcview/modules.json"}
	resp, err := w.ReadFiles(context.Background(), connect.NewRequest(&grpcviewv1.ReadFilesRequest{Paths: paths}))
	if err != nil {
		t.Fatal(err)
	}
	want := []grpcviewv1.FileError_Code{0, grpcviewv1.FileError_CODE_ENOENT, grpcviewv1.FileError_CODE_EISDIR, grpcviewv1.FileError_CODE_EACCES, 0, 0}
	results := resp.Msg.GetResults()
	if len(results) != len(paths) {
		t.Fatalf("results = %d, want %d", len(results), len(paths))
	}
	for i, r := range results {
		if r.GetPath() != paths[i] || r.GetError().GetCode() != want[i] || (want[i] == 0) != (r.GetFile() != nil) {
			t.Errorf("result %d = %v, want path %s code %v", i, r, paths[i], want[i])
		}
		if want[i] != 0 && r.GetError().GetPath() != paths[i] {
			t.Errorf("result %d error path = %q", i, r.GetError().GetPath())
		}
	}
}

func TestFilesMergeFailureIsEIOUnderTheMounts(t *testing.T) {
	w, _ := exampleFixture(t)
	ctx := context.Background()
	coll, err := w.store.Open(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	const oneID, twoID = "upload:one.binpb", "upload:two.binpb"
	if err := coll.PutDescriptorState(ctx, store.DescriptorState{
		Sources: []*grpcviewv1.DescriptorSource{
			{Id: oneID, Source: &grpcviewv1.DescriptorSource_Upload{Upload: &grpcviewv1.Upload{FileName: "one.binpb"}}},
			{Id: twoID, Source: &grpcviewv1.DescriptorSource_Upload{Upload: &grpcviewv1.Upload{FileName: "two.binpb"}}},
		},
		Resolves: map[string]*grpcviewstorev1.ResolvedSource{
			oneID: {Id: oneID, DescriptorSet: dependentSet()},
			twoID: {Id: twoID, DescriptorSet: commonSet()},
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/example/.grpcview/proto", "/example/.grpcview/proto/dup/common.proto", "/example/.grpcview/types"} {
		_, err := w.Stat(ctx, connect.NewRequest(&grpcviewv1.StatRequest{Path: p}))
		if fe := fileErrorOf(t, err); fe.GetCode() != grpcviewv1.FileError_CODE_EIO || !strings.Contains(fe.GetMessage(), "cannot be merged") {
			t.Errorf("Stat(%s) = %v, want EIO naming the merge error", p, fe)
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("Stat(%s) code = %v", p, connect.CodeOf(err))
		}
	}
	readFile(t, w, "/example/tree/users/get/body.ts")
	all := readDir(t, w, "/example", true)
	if all[".grpcview/proto"] != dirFile || all["tree/users/get/body.ts"] != regularFile {
		t.Errorf("recursive listing under a merge failure = %v, want the real tree and the mount dirs", all)
	}
	for name := range all {
		if strings.HasPrefix(name, ".grpcview/proto/") {
			t.Errorf("listed %s under a failed mount", name)
		}
	}
}

func TestFilesRootCollectionSharesTheMountDir(t *testing.T) {
	root := t.TempDir()
	w := newFilesWorkspace(t, root)
	ctx := context.Background()
	coll, err := w.store.Open(ctx, ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := coll.Create(ctx, "root"); err != nil {
		t.Fatal(err)
	}
	uploadTo(t, w, ctx, ".", fileDescriptorSet(t, "grpcview/echo/v1/echo.proto"))
	got := readDir(t, w, "/.grpcview", false)
	if len(got) != 3 || got["modules.json"] != regularFile || got["proto"] != dirFile || got["types"] != dirFile {
		t.Errorf("/.grpcview = %v, want modules.json, proto, types", got)
	}
	readFile(t, w, "/.grpcview/proto/grpcview/echo/v1/echo.proto")
	readFile(t, w, "/.grpcview/modules.json")
	if top := readDir(t, w, "/", false); top[".grpcview"] != dirFile {
		t.Errorf("/ = %v, want one .grpcview", top)
	}
}

func TestFilesModuleCapIsEIO(t *testing.T) {
	root := t.TempDir()
	w := newFilesWorkspace(t, root)
	for i := range maxWorkspaceModuleFiles + 1 {
		writeModuleFile(t, root, fmt.Sprintf("m/%04d.ts", i), "x\n")
	}
	_, err := w.ReadFile(context.Background(), connect.NewRequest(&grpcviewv1.ReadFileRequest{Path: "/.grpcview/modules.json"}))
	if fe := fileErrorOf(t, err); fe.GetCode() != grpcviewv1.FileError_CODE_EIO || !strings.Contains(fe.GetMessage(), fmt.Sprint(maxWorkspaceModuleFiles)) {
		t.Errorf("modules.json over the cap = %v, want EIO naming the cap", fe)
	}
	if _, err := (Workspace{root: root}).ListWorkspaceModules(context.Background(), connect.NewRequest(&grpcviewv1.ListWorkspaceModulesRequest{})); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Errorf("ListWorkspaceModules over the cap = %v, want ResourceExhausted", err)
	}
}

func TestFileMountsEpochs(t *testing.T) {
	root := t.TempDir()
	w := newFilesWorkspace(t, root)
	m := w.files
	m.epoch("a")
	m.epoch("a/b")
	m.bump(store.Event{Paths: []string{"/a/b/x.ts"}})
	m.bump(store.Event{Paths: []string{"/ab/x.ts"}})
	if a, ab, ws := m.epoch("a"), m.epoch("a/b"), m.epoch(""); a != 1 || ab != 1 || ws != 2 {
		t.Errorf("epochs a, a/b, workspace = %d, %d, %d; want 1, 1, 2", a, ab, ws)
	}
	m.bump(store.Event{Resync: true})
	if a := m.epoch("a"); a != 2 {
		t.Errorf("epoch after Resync = %d, want 2", a)
	}

	// modules.json is memoized until an event.
	writeModuleFile(t, root, "one.ts", "1")
	first := readFile(t, w, "/.grpcview/modules.json").GetContent()
	writeModuleFile(t, root, "two.ts", "2")
	if got := readFile(t, w, "/.grpcview/modules.json").GetContent(); got != first {
		t.Errorf("modules.json changed without an event: %s", got)
	}
	m.bump(store.Event{Paths: []string{"/two.ts"}})
	if got := readFile(t, w, "/.grpcview/modules.json").GetContent(); got == first || !strings.Contains(got, "/two.ts") {
		t.Errorf("modules.json after an event = %s, want two.ts listed", got)
	}
}

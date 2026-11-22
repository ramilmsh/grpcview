package workspace

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

// FileService reads: the workspace as a filesystem (#87 D1). Real paths come from disk; the mounts
// under .grpcview/ are virtual. Writes and Watch are #90 and #91.

const (
	mountProto   = "proto"
	mountTypes   = "types"
	modulesFile  = "modules.json"
	dirFile      = grpcviewv1.FileType_FILE_TYPE_DIRECTORY
	regularFile  = grpcviewv1.FileType_FILE_TYPE_FILE
	errnoENOENT  = grpcviewv1.FileError_CODE_ENOENT
	errnoEIO     = grpcviewv1.FileError_CODE_EIO
	errnoEISDIR  = grpcviewv1.FileError_CODE_EISDIR
	errnoENOTDIR = grpcviewv1.FileError_CODE_ENOTDIR
)

// fileError is an errno-coded failure; toFileError passes it through.
type fileError struct {
	code grpcviewv1.FileError_Code
	msg  string
}

func (e *fileError) Error() string { return e.msg }

var errNoEntry = &fileError{errnoENOENT, "no such file or directory"}

// eio wraps err as EIO, its message without Connect's code prefix.
func eio(err error) error {
	if ce := new(connect.Error); errors.As(err, &ce) {
		return &fileError{errnoEIO, ce.Message()}
	}
	return &fileError{errnoEIO, err.Error()}
}

// fileMounts memoizes mount content per epoch: hub events under a collection bump its epoch, and every
// event bumps the workspace's (key ""). #84's types digest keys on epoch(coll); #91 adds fsnotify events.
type fileMounts struct {
	cancel context.CancelFunc

	mu      sync.Mutex
	epochs  map[string]uint64
	modules *modulesSnapshot
}

type modulesSnapshot struct {
	epoch   uint64
	content []byte
}

func newFileMounts(s *store.Store) *fileMounts {
	ctx, cancel := context.WithCancel(context.Background())
	m := &fileMounts{cancel: cancel, epochs: map[string]uint64{"": 0}}
	events := s.Subscribe(ctx)
	go func() {
		for ev := range events {
			m.bump(ev)
		}
	}()
	return m
}

func (m *fileMounts) close() { m.cancel() }

func (m *fileMounts) bump(ev store.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.epochs {
		prefix := "/" + id
		if id == "" || id == "." || ev.Resync || slices.ContainsFunc(ev.Paths, func(p string) bool {
			return p == prefix || strings.HasPrefix(p, prefix+"/")
		}) {
			m.epochs[id]++
		}
	}
}

// epoch of collection id, or of the whole workspace for "". Tracked from the first ask: nothing is
// memoized before it.
func (m *fileMounts) epoch(id string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.epochs[id]
	if !ok {
		m.epochs[id] = 0
	}
	return e
}

// modulesJSON is /.grpcview/modules.json: the module scan as {paths, digest}, memoized per workspace epoch.
func (w Workspace) modulesJSON() ([]byte, error) {
	m := w.files
	m.mu.Lock()
	epoch, snap := m.epochs[""], m.modules
	m.mu.Unlock()
	if snap != nil && snap.epoch == epoch {
		return snap.content, nil
	}
	modules, err := scanWorkspaceModules(w.store.Root())
	if err != nil {
		if errors.Is(err, errWorkspaceModuleCapHit) {
			return nil, eio(err)
		}
		return nil, &fileError{errnoEIO, "workspace module scan failed"}
	}
	doc := struct {
		Paths  []string `json:"paths"`
		Digest string   `json:"digest"`
	}{Paths: make([]string, 0, len(modules))}
	h := sha256.New()
	for _, mod := range modules {
		p := "/" + mod.GetPath()
		doc.Paths = append(doc.Paths, p)
		writeLenPrefixed(h, []byte(p))
		writeLenPrefixed(h, []byte(mod.GetContent()))
	}
	doc.Digest = hex.EncodeToString(h.Sum(nil))
	content, err := json.Marshal(doc)
	if err != nil {
		return nil, eio(err)
	}
	m.mu.Lock()
	if m.epochs[""] == epoch {
		m.modules = &modulesSnapshot{epoch: epoch, content: content}
	}
	m.mu.Unlock()
	return content, nil
}

type fsEntry struct {
	name string
	typ  grpcviewv1.FileType
	link bool // a symlink: listed, never descended into
}

// fsNode is one resolved path: real when abs is set, else virtual.
type fsNode struct {
	typ     grpcviewv1.FileType
	abs     string
	info    fs.FileInfo
	content []byte    // virtual file
	entries []fsEntry // virtual directory
	label   string
}

// fsView reads each mount at most once, so one call never mixes mount versions.
type fsView struct {
	w     Workspace
	ctx   context.Context
	state string // canonical state root, "" if none
	defs  map[string]defsResult

	modulesOnce sync.Once
	modules     []byte
	modulesErr  error
}

type defsResult struct {
	defs *definitions
	err  error
}

func (w Workspace) fsView(ctx context.Context) *fsView {
	return &fsView{w: w, ctx: ctx, state: wsroot.Canonical(w.store.StateRoot()), defs: map[string]defsResult{}}
}

// hiddenSegment: invisible, or a .grpcview that is not a mount; disk beneath one is never read.
func hiddenSegment(seg string) bool { return seg == store.MountDirName || store.Hidden(seg) }

// lookup resolves a workspace-rooted path. Errors are *fileError or map through toFileError.
func (v *fsView) lookup(p string) (*fsNode, error) {
	r, err := v.w.store.ResolvePath(p)
	if err != nil {
		return nil, err
	}
	if r.Kind == store.PathMount {
		return v.mount(r)
	}
	rel := strings.TrimPrefix(path.Clean(p), "/")
	if slices.ContainsFunc(strings.Split(rel, "/"), hiddenSegment) {
		return nil, errNoEntry
	}
	abs, err := wsroot.Confine(v.w.store.Root(), filepath.FromSlash(rel))
	if errors.Is(err, wsroot.ErrEscapes) {
		return nil, err
	}
	if err != nil {
		return nil, errNoEntry
	}
	return v.real(abs)
}

// real stats abs, confined already, refusing what the FS does not serve.
func (v *fsView) real(abs string) (*fsNode, error) {
	if !v.visible(abs) {
		return nil, errNoEntry
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	switch {
	case info.IsDir():
		return &fsNode{typ: dirFile, abs: abs, info: info}, nil
	case info.Mode().IsRegular():
		return &fsNode{typ: regularFile, abs: abs, info: info}, nil
	}
	return nil, errNoEntry
}

// visible: abs (symlink-free, inside the root) crosses no hidden segment and is not the state root.
func (v *fsView) visible(abs string) bool {
	if v.state != "" && wsroot.Within(v.state, abs) {
		return false
	}
	rel, err := filepath.Rel(v.w.store.Root(), abs)
	if err != nil {
		return false
	}
	return rel == "." || !slices.ContainsFunc(strings.Split(filepath.ToSlash(rel), "/"), hiddenSegment)
}

// mount routes a PathMount: r.File is ".grpcview/..." in both the collection and the workspace form.
func (v *fsView) mount(r store.ResolvedPath) (*fsNode, error) {
	sub := strings.Split(r.File, "/")[1:]
	workspaceLevel := r.Collection == "" || r.Collection == "."
	coll := r.Collection
	switch {
	case len(sub) == 0:
		var entries []fsEntry
		if workspaceLevel {
			entries = append(entries, fsEntry{name: modulesFile, typ: regularFile})
		}
		if coll != "" {
			entries = append(entries, fsEntry{name: mountProto, typ: dirFile}, fsEntry{name: mountTypes, typ: dirFile})
		}
		return virtualDir(entries), nil
	case workspaceLevel && len(sub) == 1 && sub[0] == modulesFile:
		content, err := v.modulesJSON()
		if err != nil {
			return nil, err
		}
		return &fsNode{typ: regularFile, content: content}, nil
	case coll != "" && sub[0] == mountProto:
		protos, err := v.protos(coll)
		if err != nil {
			return nil, err
		}
		return protoNode(protos, strings.Join(sub[1:], "/"))
	case coll != "" && sub[0] == mountTypes:
		if _, err := v.definitions(coll); err != nil {
			return nil, err
		}
		// Routing hook only: #84 fills it from TypeLayout(coll) (#79).
		if len(sub) == 1 {
			return virtualDir(nil), nil
		}
	}
	return nil, errNoEntry
}

func virtualDir(entries []fsEntry) *fsNode {
	slices.SortFunc(entries, func(a, b fsEntry) int { return cmp.Compare(a.name, b.name) })
	return &fsNode{typ: dirFile, entries: entries}
}

// protoNode is rel ("" for the mount root) in the merged proto files: a file, or a directory of them.
func protoNode(files []*grpcviewv1.ProtoDefinitionFile, rel string) (*fsNode, error) {
	prefix := ""
	if rel != "" {
		prefix = rel + "/"
	}
	seen := map[string]bool{}
	var entries []fsEntry
	for _, f := range files {
		if f.GetPath() == rel {
			return &fsNode{typ: regularFile, content: []byte(f.GetContent()), label: f.GetSource()}, nil
		}
		tail, ok := strings.CutPrefix(f.GetPath(), prefix)
		if !ok {
			continue
		}
		name, _, nested := strings.Cut(tail, "/")
		if seen[name] {
			continue
		}
		seen[name] = true
		typ := regularFile
		if nested {
			typ = dirFile
		}
		entries = append(entries, fsEntry{name: name, typ: typ})
	}
	if rel != "" && len(entries) == 0 {
		return nil, errNoEntry
	}
	return virtualDir(entries), nil
}

func (v *fsView) definitions(coll string) (*definitions, error) {
	if r, ok := v.defs[coll]; ok {
		return r.defs, r.err
	}
	d, err := v.loadDefinitions(coll)
	v.defs[coll] = defsResult{defs: d, err: err}
	return d, err
}

func (v *fsView) loadDefinitions(id string) (*definitions, error) {
	coll, err := v.w.store.Open(v.ctx, id)
	if err != nil {
		return nil, eio(err)
	}
	sources, err := coll.Sources(v.ctx)
	if err != nil {
		return nil, eio(err)
	}
	// definitionsFor, like GetDefinitions: a hand-edited grpcview.json must not be served stale.
	defs, err := v.w.definitionsFor(v.ctx, coll, sources)
	if err != nil {
		return nil, eio(err)
	}
	if defs.mergeErr != nil {
		return nil, eio(defs.mergeErr)
	}
	return defs, nil
}

func (v *fsView) protos(coll string) ([]*grpcviewv1.ProtoDefinitionFile, error) {
	defs, err := v.definitions(coll)
	if err != nil {
		return nil, err
	}
	protos, err := defs.protoFiles()
	if err != nil {
		return nil, eio(err)
	}
	return protos, nil
}

func (v *fsView) modulesJSON() ([]byte, error) {
	v.modulesOnce.Do(func() { v.modules, v.modulesErr = v.w.modulesJSON() })
	return v.modules, v.modulesErr
}

// read is n's content and Stat. Text only: content travels as a proto string.
func (v *fsView) read(n *fsNode) ([]byte, *grpcviewv1.Stat, error) {
	st := &grpcviewv1.Stat{Type: n.typ, Label: n.label}
	if n.info != nil {
		st.MtimeNs = n.info.ModTime().UnixNano()
	}
	if n.typ == dirFile {
		return nil, st, nil
	}
	content := n.content
	if n.abs != "" {
		var err error
		if content, err = os.ReadFile(n.abs); err != nil {
			return nil, nil, err
		}
	}
	if !utf8.Valid(content) {
		return nil, nil, &fileError{errnoEIO, "not UTF-8 text"}
	}
	sum := sha256.Sum256(content)
	st.Size, st.Version = int64(len(content)), hex.EncodeToString(sum[:])
	return content, st, nil
}

// children of directory n at p; a real directory hosting a mount gains its virtual .grpcview.
func (v *fsView) children(p string, n *fsNode) ([]fsEntry, error) {
	if n.typ != dirFile {
		return nil, &fileError{errnoENOTDIR, "not a directory"}
	}
	if n.abs == "" {
		return n.entries, nil
	}
	des, err := os.ReadDir(n.abs)
	if err != nil {
		return nil, err
	}
	var out []fsEntry
	for _, de := range des {
		if hiddenSegment(de.Name()) {
			continue
		}
		e := fsEntry{name: de.Name(), link: de.Type()&fs.ModeSymlink != 0}
		child := filepath.Join(n.abs, e.name)
		switch {
		case e.link:
			target, err := wsroot.Confine(v.w.store.Root(), child)
			if err != nil {
				continue
			}
			c, err := v.real(target)
			if err != nil {
				continue
			}
			e.typ = c.typ
		case !v.visible(child):
			continue
		case de.IsDir():
			e.typ = dirFile
		case de.Type().IsRegular():
			e.typ = regularFile
		default:
			continue
		}
		out = append(out, e)
	}
	if v.hostsMount(p, des) {
		out = append(out, fsEntry{name: store.MountDirName, typ: dirFile})
	}
	slices.SortFunc(out, func(a, b fsEntry) int { return cmp.Compare(a.name, b.name) })
	return out, nil
}

// hostsMount: p (listing des) is the workspace root or a collection root, so it holds a virtual .grpcview.
func (v *fsView) hostsMount(p string, des []fs.DirEntry) bool {
	if p == "/" {
		return true
	}
	if !slices.ContainsFunc(des, func(de fs.DirEntry) bool { return de.Name() == store.CollectionFileName }) {
		return false
	}
	r, err := v.w.store.ResolvePath(p)
	return err == nil && r.Collection != "" && r.File == ""
}

// walk appends n's children, and with recursive all descendants, named relative to the listed
// directory. A mount that fails (a merge error) is listed but not descended into: the real tree stays
// browsable.
func (v *fsView) walk(p string, n *fsNode, prefix string, recursive bool, out *[]*grpcviewv1.ReadDirectoryResponse_Entry) error {
	entries, err := v.children(p, n)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := prefix + e.name
		*out = append(*out, &grpcviewv1.ReadDirectoryResponse_Entry{Name: name, Type: e.typ})
		if !recursive || e.typ != dirFile || e.link {
			continue
		}
		childPath := path.Join(p, e.name)
		var child *fsNode
		if n.abs == "" || e.name == store.MountDirName {
			if child, err = v.lookup(childPath); err != nil {
				continue
			}
		} else {
			child = &fsNode{typ: dirFile, abs: filepath.Join(n.abs, e.name)}
		}
		if err := v.walk(childPath, child, name+"/", true, out); err != nil {
			return err
		}
	}
	return nil
}

// fail is err as a Connect error carrying its FileError, host paths scrubbed.
func (w Workspace) fail(p string, err error) error { return fileConnectError(w.fileError(p, err)) }

func (w Workspace) fileError(p string, err error) *grpcviewv1.FileError {
	fe := toFileError(p, err)
	if root := w.store.Root(); root != "" {
		fe.Message = strings.ReplaceAll(fe.GetMessage(), root, "")
	}
	return fe
}

func (w Workspace) Stat(ctx context.Context, request *connect.Request[grpcviewv1.StatRequest]) (*connect.Response[grpcviewv1.StatResponse], error) {
	p := request.Msg.GetPath()
	v := w.fsView(ctx)
	n, err := v.lookup(p)
	if err != nil {
		return nil, w.fail(p, err)
	}
	_, st, err := v.read(n)
	if err != nil {
		return nil, w.fail(p, err)
	}
	return connect.NewResponse(&grpcviewv1.StatResponse{Stat: st}), nil
}

func (w Workspace) ReadDirectory(ctx context.Context, request *connect.Request[grpcviewv1.ReadDirectoryRequest]) (*connect.Response[grpcviewv1.ReadDirectoryResponse], error) {
	p := request.Msg.GetPath()
	v := w.fsView(ctx)
	n, err := v.lookup(p)
	if err != nil {
		return nil, w.fail(p, err)
	}
	var out []*grpcviewv1.ReadDirectoryResponse_Entry
	if err := v.walk(path.Clean(p), n, "", request.Msg.GetRecursive(), &out); err != nil {
		return nil, w.fail(p, err)
	}
	return connect.NewResponse(&grpcviewv1.ReadDirectoryResponse{Entries: out}), nil
}

func (v *fsView) readFile(p string) ([]byte, *grpcviewv1.Stat, error) {
	n, err := v.lookup(p)
	if err != nil {
		return nil, nil, err
	}
	if n.typ == dirFile {
		return nil, nil, &fileError{errnoEISDIR, "is a directory"}
	}
	return v.read(n)
}

func (w Workspace) ReadFile(ctx context.Context, request *connect.Request[grpcviewv1.ReadFileRequest]) (*connect.Response[grpcviewv1.ReadFileResponse], error) {
	p := request.Msg.GetPath()
	content, st, err := w.fsView(ctx).readFile(p)
	if err != nil {
		return nil, w.fail(p, err)
	}
	return connect.NewResponse(&grpcviewv1.ReadFileResponse{Content: string(content), Stat: st}), nil
}

func (w Workspace) ReadFiles(ctx context.Context, request *connect.Request[grpcviewv1.ReadFilesRequest]) (*connect.Response[grpcviewv1.ReadFilesResponse], error) {
	v := w.fsView(ctx)
	results := make([]*grpcviewv1.ReadFilesResponse_Result, 0, len(request.Msg.GetPaths()))
	for _, p := range request.Msg.GetPaths() {
		r := &grpcviewv1.ReadFilesResponse_Result{Path: p}
		if content, st, err := v.readFile(p); err != nil {
			r.Result = &grpcviewv1.ReadFilesResponse_Result_Error{Error: w.fileError(p, err)}
		} else {
			r.Result = &grpcviewv1.ReadFilesResponse_Result_File{File: &grpcviewv1.ReadFilesResponse_File{Content: string(content), Stat: st}}
		}
		results = append(results, r)
	}
	return connect.NewResponse(&grpcviewv1.ReadFilesResponse{Results: results}), nil
}

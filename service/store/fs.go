package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func (c *Collection) Load(ctx context.Context) (*grpcviewv1.Collection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.load(ctx)
}

func (c *Collection) load(_ context.Context) (*grpcviewv1.Collection, error) {
	col, err := c.readCollection()
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	rootItems, err := c.walkFolder(c.treeRoot(), col.GetItems())
	if err != nil {
		return nil, err
	}
	scripts, err := c.readScripts()
	if err != nil {
		return nil, err
	}
	defs, err := c.store.workspaceDefinitions()
	if err != nil {
		return nil, err
	}

	name := cmp.Or(col.GetName(), c.defaultName())
	return &grpcviewv1.Collection{
		Id:   c.id,
		Name: name,
		Item: &grpcviewv1.Item{
			Name:    name,
			Content: &grpcviewv1.Item_Folder{Folder: &grpcviewv1.Folder{Items: rootItems}},
		},
		Sources: diskToWireSources(col.GetSources(), defs),
		Scripts: scripts,
	}, nil
}

func (c *Collection) Summary(_ context.Context) (name string, sourceCount int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	col, err := c.readCollection()
	if errors.Is(err, os.ErrNotExist) {
		return "", 0, ErrNotFound
	}
	if err != nil {
		return "", 0, err
	}
	return cmp.Or(col.GetName(), c.defaultName()), len(col.GetSources()), nil
}

func (c *Collection) Sources(_ context.Context) ([]*grpcviewv1.DescriptorSource, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	col, err := c.readCollection()
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defs, err := c.store.workspaceDefinitions()
	if err != nil {
		return nil, err
	}
	return diskToWireSources(col.GetSources(), defs), nil
}

func (c *Collection) Scripts(_ context.Context) ([]*grpcviewv1.Script, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return nil, err
	}
	return c.readScripts()
}

func (c *Collection) Create(_ context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fileExists(c.collectionFilePath()) {
		return fmt.Errorf("%w: %q", ErrCollectionExists, c.id)
	}
	if err := os.MkdirAll(c.treeRoot(), 0o755); err != nil {
		return err
	}
	if name == "" {
		name = c.defaultName()
	}
	seeded, err := c.seededSources()
	if err != nil {
		return err
	}
	return c.writeCollection(&grpcviewstorev1.Collection{Name: name, Sources: seeded})
}

func (c *Collection) SetName(_ context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	col, err := c.readCollection()
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if name == "" {
		name = c.defaultName()
	}
	col.Name = name
	return c.writeCollection(col)
}

func (c *Collection) seededSources() ([]*grpcviewstorev1.DescriptorSource, error) {
	ws, err := c.store.readWorkspaceManifest()
	if err != nil {
		return nil, err
	}
	ids := ws.GetDefaults().GetSources()
	if len(ids) == 0 {
		return nil, nil
	}
	defs := c.store.definitionSet(ws.GetSources())
	out := make([]*grpcviewstorev1.DescriptorSource, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if defs[id] == nil {
			c.logger.Warn("skipping a default descriptor source the workspace manifest does not define",
				"id", id, "manifest", WorkspaceFileName)
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, &grpcviewstorev1.DescriptorSource{Id: id})
	}
	return out, nil
}

func (c *Collection) CreateFolder(_ context.Context, parent []string, name string) error {
	return c.createItem(parent, name, func(itemDir string) error {
		return writeMessage(filepath.Join(itemDir, folderFileName), &grpcviewstorev1.Folder{
			Meta: &grpcviewstorev1.ItemMeta{Name: name},
		})
	})
}

// Always writes all three files, so "file absent" is never a state a reader has to
// interpret. metadata.ts is seeded EMPTY, never EmptyBody: resolveInvokeMetadata treats any
// non-empty script as authoritative and skips the folder-metadata inherit fold, so a "{}"
// seed would silently break folder-metadata inheritance for every new request.
func (c *Collection) CreateRequest(_ context.Context, parent []string, name, service, method string) error {
	return c.createItem(parent, name, func(itemDir string) error {
		if err := writeMessage(filepath.Join(itemDir, RequestFileName), &grpcviewstorev1.Request{
			Meta:    &grpcviewstorev1.ItemMeta{Name: name},
			Service: service,
			Method:  method,
		}); err != nil {
			return err
		}
		if err := writeSourceFile(requestBodyPath(itemDir), EmptyBody); err != nil {
			return err
		}
		return writeSourceFile(requestMetadataPath(itemDir), "")
	})
}

func (c *Collection) createItem(parent []string, name string, write func(itemDir string) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	parentDir, err := c.resolveFolder(parent)
	if err != nil {
		return err
	}
	present, err := c.readChildren(parentDir)
	if err != nil {
		return err
	}
	if _, ok := findByName(present, name); ok {
		return fmt.Errorf("%w: %q", ErrAlreadyExists, name)
	}

	slug := uniqueSlug(name, slugSet(present))
	base, err := c.reconciledSlugsFrom(parentDir, present)
	if err != nil {
		return err
	}
	itemDir := filepath.Join(parentDir, slug)
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		return err
	}
	if err := write(itemDir); err != nil {
		return err
	}
	return c.writeOrder(parentDir, append(base, slug))
}

func (c *Collection) UpdateRequest(_ context.Context, parent []string, name string, patch RequestPatch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	parentDir, err := c.resolveFolder(parent)
	if err != nil {
		return err
	}
	present, err := c.readChildren(parentDir)
	if err != nil {
		return err
	}
	ch, ok := findByName(present, name)
	if !ok {
		return fmt.Errorf("%w: %q", ErrItemNotFound, name)
	}
	if ch.kind != kindRequest {
		return fmt.Errorf("%w: %q", ErrNotARequest, name)
	}
	itemDir := filepath.Join(parentDir, ch.slug)

	if patch.Name == nil && patch.Service == nil && patch.Method == nil && patch.DraftBody == nil && patch.DraftMetadataScript == nil && !patch.SetMiddleware && !patch.SetTarget {
		return nil
	}

	// Validate (and mutate the in-memory copy) before any write, so a rejected rename never
	// leaves a body/metadata write applied with nothing on request.json to match it.
	dr := ch.request
	if patch.Name != nil && *patch.Name != name {
		if _, exists := findByName(present, *patch.Name); exists {
			return fmt.Errorf("%w: %q", ErrAlreadyExists, *patch.Name)
		}
		if dr.Meta == nil {
			dr.Meta = &grpcviewstorev1.ItemMeta{}
		}
		dr.Meta.Name = *patch.Name
	}
	if patch.Service != nil {
		dr.Service = *patch.Service
	}
	if patch.Method != nil {
		dr.Method = *patch.Method
	}
	if patch.SetMiddleware {
		dr.Middleware = patch.Middleware
	}
	if patch.SetTarget {
		dr.Target = serverToTarget(patch.Target)
	}

	if patch.DraftBody != nil {
		if err := writeSourceFile(requestBodyPath(itemDir), *patch.DraftBody); err != nil {
			return err
		}
	}
	if patch.DraftMetadataScript != nil {
		if err := writeSourceFile(requestMetadataPath(itemDir), *patch.DraftMetadataScript); err != nil {
			return err
		}
	}

	// A body/metadata-only patch must not rewrite request.json: a body keystroke would
	// otherwise touch a file it has nothing to do with.
	if patch.Name == nil && patch.Service == nil && patch.Method == nil && !patch.SetMiddleware && !patch.SetTarget {
		return nil
	}
	return writeMessage(filepath.Join(itemDir, RequestFileName), dr)
}

func (c *Collection) UpdateFolder(_ context.Context, parent []string, name string, patch FolderPatch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	parentDir, err := c.resolveFolder(parent)
	if err != nil {
		return err
	}
	present, err := c.readChildren(parentDir)
	if err != nil {
		return err
	}
	ch, ok := findByName(present, name)
	if !ok {
		return fmt.Errorf("%w: %q", ErrItemNotFound, name)
	}
	if ch.kind != kindFolder {
		return fmt.Errorf("%w: %q", ErrNotAFolder, name)
	}

	if patch.Name == nil && patch.DraftMetadataScript == nil {
		return nil
	}
	p := filepath.Join(parentDir, ch.slug, folderFileName)
	ff := ch.folder
	if patch.Name != nil && *patch.Name != name {
		if _, exists := findByName(present, *patch.Name); exists {
			return fmt.Errorf("%w: %q", ErrAlreadyExists, *patch.Name)
		}
		if ff.Meta == nil {
			ff.Meta = &grpcviewstorev1.ItemMeta{}
		}
		ff.Meta.Name = *patch.Name
	}
	if patch.DraftMetadataScript != nil {
		ff.DraftMetadataScript = *patch.DraftMetadataScript
	}
	return writeMessage(p, ff)
}

func (c *Collection) FolderMetadataChain(_ context.Context, path []string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return nil, err
	}
	scripts := make([]string, 0, len(path))
	dir := c.treeRoot()
	for _, name := range path {
		children, err := c.readChildren(dir)
		if err != nil {
			return nil, err
		}
		ch, ok := findByName(children, name)
		if !ok {
			return nil, fmt.Errorf("%w: folder %q", ErrItemNotFound, name)
		}
		if ch.kind != kindFolder {
			return nil, fmt.Errorf("%w: %q", ErrNotAFolder, name)
		}
		scripts = append(scripts, ch.folder.GetDraftMetadataScript())
		dir = filepath.Join(dir, ch.slug)
	}
	return scripts, nil
}

// Callers must hold c.mu and have already called c.ensureExists.
func (c *Collection) resolveChild(parent []string, name string) (parentDir string, present []childEntry, ch childEntry, ok bool, err error) {
	parentDir, err = c.resolveFolder(parent)
	if err != nil {
		return "", nil, childEntry{}, false, err
	}
	present, err = c.readChildren(parentDir)
	if err != nil {
		return "", nil, childEntry{}, false, err
	}
	ch, ok = findByName(present, name)
	return parentDir, present, ch, ok, nil
}

func (c *Collection) resolveRequestChild(parent []string, name string) (parentDir string, ch childEntry, err error) {
	parentDir, _, ch, ok, err := c.resolveChild(parent, name)
	if err != nil {
		return "", childEntry{}, err
	}
	if !ok {
		return "", childEntry{}, fmt.Errorf("%w: %q", ErrItemNotFound, name)
	}
	if ch.kind != kindRequest {
		return "", childEntry{}, fmt.Errorf("%w: %q", ErrNotARequest, name)
	}
	return parentDir, ch, nil
}

func (c *Collection) ResolveRequest(ctx context.Context, parent []string, name string) (*grpcviewv1.Request, error) {
	req, _, _, err := c.ResolveRequestFiles(ctx, parent, name)
	return req, err
}

// ResolveRequestFiles is what ResolveRequest delegates to, plus the two source paths made
// relative to the STORE ROOT (not the collection root), so an error naming them reads like
// "example/tree/workspace/listcollections/body.ts" and leaks no home directory.
func (c *Collection) ResolveRequestFiles(_ context.Context, parent []string, name string) (req *grpcviewv1.Request, bodyPath, metadataPath string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err = c.ensureExists(); err != nil {
		return nil, "", "", err
	}
	parentDir, ch, err := c.resolveRequestChild(parent, name)
	if err != nil {
		return nil, "", "", err
	}
	itemDir := filepath.Join(parentDir, ch.slug)

	body, err := readSourceFile(requestBodyPath(itemDir))
	if err != nil {
		return nil, "", "", err
	}
	metadataScript, err := readSourceFile(requestMetadataPath(itemDir))
	if err != nil {
		return nil, "", "", err
	}
	bodyPath, err = c.storeRelPath(requestBodyPath(itemDir))
	if err != nil {
		return nil, "", "", err
	}
	metadataPath, err = c.storeRelPath(requestMetadataPath(itemDir))
	if err != nil {
		return nil, "", "", err
	}
	return diskToWireRequest(ch.name, ch.request, body, metadataScript), bodyPath, metadataPath, nil
}

func (c *Collection) storeRelPath(p string) (string, error) {
	rel, err := filepath.Rel(c.store.Root(), p)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func (c *Collection) RequestMiddleware(_ context.Context, parent []string, name string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return nil, err
	}
	_, ch, err := c.resolveRequestChild(parent, name)
	if err != nil {
		return nil, err
	}
	return ch.request.GetMiddleware(), nil
}

func (c *Collection) AppendHistory(_ context.Context, parent []string, name string, entry *grpcviewv1.History, max int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	parentDir, ch, err := c.resolveRequestChild(parent, name)
	if err != nil {
		return err
	}
	histPath, err := c.historyFilePath(filepath.Join(parentDir, ch.slug))
	if err != nil {
		return err
	}

	entries, err := readHistoryFile(histPath)
	if err != nil {
		return err
	}
	entries = append(entries, entry)
	if max > 0 && len(entries) > max {
		dropped := len(entries) - max
		c.logger.Info("capping request history", "request", name, "dropped", dropped, "cap", max)
		entries = entries[dropped:]
	}
	return writeHistoryFile(histPath, entries)
}

func (c *Collection) Delete(_ context.Context, parent []string, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	parentDir, present, ch, ok, err := c.resolveChild(parent, name)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	base, err := c.reconciledSlugsFrom(parentDir, present)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(parentDir, ch.slug)); err != nil {
		return err
	}
	return c.writeOrder(parentDir, slices.DeleteFunc(base, func(s string) bool { return s == ch.slug }))
}

func (c *Collection) Move(_ context.Context, parent []string, name string, newParent []string, before *string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	parentDir, present, ch, ok, err := c.resolveChild(parent, name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %q", ErrItemNotFound, name)
	}
	destDir, err := c.resolveFolder(newParent)
	if err != nil {
		return err
	}

	srcDir := filepath.Join(parentDir, ch.slug)
	rel, err := filepath.Rel(srcDir, destDir)
	if err != nil {
		return err
	}
	if rel == "." || !(rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return fmt.Errorf("%w: %q", ErrMoveIntoDescendant, name)
	}

	if destDir == parentDir {
		base, err := c.reconciledSlugsFrom(parentDir, present)
		if err != nil {
			return err
		}
		base = slices.DeleteFunc(base, func(s string) bool { return s == ch.slug })
		return c.writeOrder(parentDir, insertSlug(base, ch.slug, before, present))
	}

	destChildren, err := c.readChildren(destDir)
	if err != nil {
		return err
	}
	if _, exists := findByName(destChildren, ch.name); exists {
		return fmt.Errorf("%w: %q", ErrAlreadyExists, ch.name)
	}
	newSlug := uniqueSlug(ch.name, slugSet(destChildren))

	srcBase, err := c.reconciledSlugsFrom(parentDir, present)
	if err != nil {
		return err
	}
	destBase, err := c.reconciledSlugsFrom(destDir, destChildren)
	if err != nil {
		return err
	}
	// The directory must move FIRST: "moved, order not" is the half-applied state reconcileOrder
	// self-heals on the next load.
	if err := os.Rename(srcDir, filepath.Join(destDir, newSlug)); err != nil {
		return err
	}
	if err := c.writeOrder(parentDir, slices.DeleteFunc(srcBase, func(s string) bool { return s == ch.slug })); err != nil {
		return err
	}
	return c.writeOrder(destDir, insertSlug(destBase, newSlug, before, destChildren))
}

func insertSlug(slugs []string, slug string, before *string, siblings []childEntry) []string {
	if before != nil {
		if ch, ok := findByName(siblings, *before); ok {
			if i := slices.Index(slugs, ch.slug); i >= 0 {
				return slices.Insert(slugs, i, slug)
			}
		}
	}
	return append(slugs, slug)
}

type DescriptorState struct {
	Sources  []*grpcviewv1.DescriptorSource
	Resolves map[string]*grpcviewstorev1.ResolvedSource
}

// Writes the whole state under one lock — so a reader never sees a source list and a descriptor store
// that disagree — and collects what nothing points at any more. Each source is written to exactly ONE
// place, chosen by its commit_descriptors flag, which is what makes toggling that flag a move rather
// than a copy.
func (c *Collection) PutDescriptorState(_ context.Context, state DescriptorState) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}

	col, err := c.readCollection()
	if err != nil {
		return err
	}
	defs, err := c.store.workspaceDefinitions()
	if err != nil {
		return err
	}
	col.Sources = wireToDiskSources(state.Sources, defs)
	if err := c.writeCollection(col); err != nil {
		return err
	}

	c.store.blobMu.Lock()
	defer c.store.blobMu.Unlock()

	existingIndex, err := c.readDescriptorIndex()
	if err != nil {
		return err
	}
	entries := make([]*grpcviewstorev1.DescriptorIndexEntry, 0, len(state.Sources))
	keepSidecars := make(map[string]bool, len(state.Sources))
	for _, src := range state.Sources {
		id := src.GetId()
		fresh := state.Resolves[id]

		if src.GetCommitDescriptors() {
			r := fresh
			if r == nil {
				if r, err = c.storedResolve(id, existingIndex); err != nil {
					return err
				}
			}
			if r == nil {
				continue
			}
			if err := c.writeDescriptorSidecar(r); err != nil {
				return err
			}
			keepSidecars[filepath.Base(c.descriptorSidecarPath(id))] = true
			continue
		}

		if fresh == nil {
			if prev, ok := existingIndex[id]; ok {
				entries = append(entries, prev)
				continue
			}
			if fresh, err = c.readDescriptorSidecar(id); err != nil {
				return err
			}
			if fresh == nil {
				continue
			}
		}
		digest, err := c.store.putBlob(fresh.GetDescriptorSet())
		if err != nil {
			return err
		}
		entries = append(entries, &grpcviewstorev1.DescriptorIndexEntry{
			SourceId:     id,
			Digest:       digest,
			ServiceNames: fresh.GetServiceNames(),
		})
	}
	if err := c.writeDescriptorIndex(entries); err != nil {
		return err
	}
	if err := c.pruneDescriptorSidecars(keepSidecars); err != nil {
		return err
	}
	return c.store.gcBlobs()
}

// Returns what a source last resolved to from whichever location the previous write used, or nil when
// it never has. Callers must hold c.mu.
func (c *Collection) storedResolve(id string, index map[string]*grpcviewstorev1.DescriptorIndexEntry) (*grpcviewstorev1.ResolvedSource, error) {
	if sidecar, err := c.readDescriptorSidecar(id); err != nil || sidecar != nil {
		return sidecar, err
	}
	if entry, ok := index[id]; ok {
		return c.blobResolve(id, entry)
	}
	return nil, nil
}

func (c *Collection) resolveFolder(namePath []string) (string, error) {
	dir := c.treeRoot()
	for _, name := range namePath {
		children, err := c.readChildren(dir)
		if err != nil {
			return "", err
		}
		ch, ok := findByName(children, name)
		if !ok {
			return "", fmt.Errorf("%w: folder %q", ErrItemNotFound, name)
		}
		if ch.kind != kindFolder {
			return "", fmt.Errorf("%w: %q", ErrNotAFolder, name)
		}
		dir = filepath.Join(dir, ch.slug)
	}
	return dir, nil
}

func (c *Collection) readChildren(dir string) ([]childEntry, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var children []childEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		if strings.HasPrefix(slug, ".") || isReserved(slug) {
			continue
		}
		sub := filepath.Join(dir, slug)
		switch {
		case fileExists(filepath.Join(sub, folderFileName)):
			ff := &grpcviewstorev1.Folder{}
			if err := readMessage(filepath.Join(sub, folderFileName), ff); err != nil {
				return nil, err
			}
			children = append(children, childEntry{slug: slug, name: cmp.Or(ff.GetMeta().GetName(), slug), kind: kindFolder, folder: ff})
		case fileExists(filepath.Join(sub, RequestFileName)):
			rf := &grpcviewstorev1.Request{}
			if err := readRequestMessage(filepath.Join(sub, RequestFileName), rf); err != nil {
				return nil, err
			}
			children = append(children, childEntry{slug: slug, name: cmp.Or(rf.GetMeta().GetName(), slug), kind: kindRequest, request: rf})
		}
	}
	return children, nil
}

func (c *Collection) reconcile(dir string, listed []string, present []childEntry) []childEntry {
	ordered, dropped := reconcileOrder(listed, present)
	for _, slug := range dropped {
		c.logger.Warn("dropping ordered item missing on disk", "dir", dir, "slug", slug)
	}
	return ordered
}

func (c *Collection) walkFolder(dir string, listed []string) ([]*grpcviewv1.Item, error) {
	present, err := c.readChildren(dir)
	if err != nil {
		return nil, err
	}
	ordered := c.reconcile(dir, listed, present)
	items := make([]*grpcviewv1.Item, 0, len(ordered))
	for _, ch := range ordered {
		item, err := c.readItem(dir, ch)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (c *Collection) readItem(parentDir string, ch childEntry) (*grpcviewv1.Item, error) {
	dir := filepath.Join(parentDir, ch.slug)
	switch ch.kind {
	case kindFolder:
		children, err := c.walkFolder(dir, ch.folder.GetItems())
		if err != nil {
			return nil, err
		}
		return &grpcviewv1.Item{
			Name: ch.name,
			Slug: ch.slug,
			Content: &grpcviewv1.Item_Folder{Folder: &grpcviewv1.Folder{
				Items:               children,
				DraftMetadataScript: ch.folder.GetDraftMetadataScript(),
			}},
		}, nil
	case kindRequest:
		body, err := readSourceFile(requestBodyPath(dir))
		if err != nil {
			return nil, err
		}
		metadataScript, err := readSourceFile(requestMetadataPath(dir))
		if err != nil {
			return nil, err
		}
		req := diskToWireRequest(ch.name, ch.request, body, metadataScript)
		req.History = c.readHistory(dir)
		return &grpcviewv1.Item{
			Name:    ch.name,
			Slug:    ch.slug,
			Content: &grpcviewv1.Item_Request{Request: req},
		}, nil
	}
	return nil, fmt.Errorf("unknown item kind")
}

func (c *Collection) readOrder(dir string) ([]string, error) {
	if dir == c.treeRoot() {
		col, err := c.readCollection()
		if err != nil {
			return nil, err
		}
		return col.GetItems(), nil
	}
	ff := &grpcviewstorev1.Folder{}
	if err := readMessage(filepath.Join(dir, folderFileName), ff); err != nil {
		return nil, err
	}
	return ff.GetItems(), nil
}

func (c *Collection) writeOrder(dir string, slugs []string) error {
	if slugs == nil {
		slugs = []string{}
	}
	if dir == c.treeRoot() {
		col, err := c.readCollection()
		if err != nil {
			return err
		}
		col.Items = slugs
		return c.writeCollection(col)
	}
	p := filepath.Join(dir, folderFileName)
	ff := &grpcviewstorev1.Folder{}
	if err := readMessage(p, ff); err != nil {
		return err
	}
	ff.Items = slugs
	return writeMessage(p, ff)
}

func (c *Collection) reconciledSlugsFrom(dir string, present []childEntry) ([]string, error) {
	listed, err := c.readOrder(dir)
	if err != nil {
		return nil, err
	}
	ordered := c.reconcile(dir, listed, present)
	slugs := make([]string, len(ordered))
	for i, ch := range ordered {
		slugs[i] = ch.slug
	}
	return slugs, nil
}

func (c *Collection) ensureExists() error {
	_, err := os.Stat(c.collectionFilePath())
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

func (c *Collection) readCollection() (*grpcviewstorev1.Collection, error) {
	col := &grpcviewstorev1.Collection{}
	if err := readMessage(c.collectionFilePath(), col); err != nil {
		return nil, err
	}
	col.Sources = normalizeSources(col.GetSources(), c.logger)
	return col, nil
}

func (c *Collection) writeCollection(col *grpcviewstorev1.Collection) error {
	if col.SchemaVersion == 0 {
		col.SchemaVersion = schemaVersion
	}
	return writeMessage(c.collectionFilePath(), col)
}

func (c *Collection) historyFilePath(itemDir string) (string, error) {
	rel, err := filepath.Rel(c.treeRoot(), itemDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.historyRoot(), rel, historyFileName), nil
}

func (c *Collection) readHistory(itemDir string) []*grpcviewv1.History {
	histPath, err := c.historyFilePath(itemDir)
	if err != nil {
		c.logger.Warn("resolve history path", "dir", itemDir, "err", err)
		return nil
	}
	entries, err := readHistoryFile(histPath)
	if err != nil {
		c.logger.Warn("read request history", "path", histPath, "err", err)
		return nil
	}
	return entries
}

var historyMarshal = protojson.MarshalOptions{Multiline: true, Indent: "  "}

func readHistoryFile(path string) ([]*grpcviewv1.History, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	carrier := &grpcviewv1.Request{}
	if err := unmarshalOpts.Unmarshal(data, carrier); err != nil {
		return nil, fmt.Errorf("unmarshal history %s: %w", path, err)
	}
	return carrier.GetHistory(), nil
}

func writeHistoryFile(path string, entries []*grpcviewv1.History) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := historyMarshal.Marshal(&grpcviewv1.Request{History: entries})
	if err != nil {
		return fmt.Errorf("marshal history %s: %w", path, err)
	}
	return writeFileAtomic(path, append(data, '\n'), 0o644)
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

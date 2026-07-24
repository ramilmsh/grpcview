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
	"google.golang.org/protobuf/proto"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// Load assembles the full in-memory (wire) Workspace by walking tree/ in the
// order recorded by each folder's config, reading the committed sources from
// grpcview.json and the resolved-schema cache from .grpcview/. It returns
// ErrNotFound if the collection has not been created.
func (c *Collection) Load(ctx context.Context) (*grpcviewv1.Workspace, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.load(ctx)
}

func (c *Collection) load(_ context.Context) (*grpcviewv1.Workspace, error) {
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
	sources, err := diskToWireSources(col.GetSources())
	if err != nil {
		return nil, err
	}
	services, descriptorSet, err := c.readServicesCache()
	if err != nil {
		return nil, err
	}
	scripts, err := c.loadScripts(col.GetScripts())
	if err != nil {
		return nil, err
	}

	name := cmp.Or(col.GetName(), c.name)
	return &grpcviewv1.Workspace{
		Name: name,
		Item: &grpcviewv1.Item{
			Name:    name,
			Content: &grpcviewv1.Item_Folder{Folder: &grpcviewv1.Folder{Items: rootItems}},
		},
		Sources:       sources,
		Services:      services,
		Scripts:       scripts,
		DescriptorSet: descriptorSet,
	}, nil
}

// Sources returns just the committed descriptor sources from the manifest,
// without walking the request tree or reading the schema cache — the cheap read
// Invoke's target resolution needs. It returns ErrNotFound if the collection has
// not been created.
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
	return diskToWireSources(col.GetSources())
}

// Scripts returns just the collection's ordered scripts (manifest order + the
// scripts/ directory), without walking the request tree or reading the schema
// cache — the cheap read the invoke path's token resolution needs, mirroring
// Sources. It returns ErrNotFound if the collection has not been created.
func (c *Collection) Scripts(_ context.Context) ([]*grpcviewv1.Script, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	col, err := c.readCollection()
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return c.loadScripts(col.GetScripts())
}

// EnsureCreated creates an empty collection (grpcview.json, tree/, .gitignore)
// if one does not already exist. Used by the Get RPC, which auto-creates.
func (c *Collection) EnsureCreated(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fileExists(c.collectionFilePath()) {
		return nil
	}
	if err := os.MkdirAll(c.treeRoot(), 0o755); err != nil {
		return err
	}
	if err := c.ensureGitignore(); err != nil {
		return err
	}
	return c.writeCollection(&grpcviewstorev1.Collection{})
}

// CreateFolder creates a folder named name inside the folder addressed by the
// display-name path parent.
func (c *Collection) CreateFolder(_ context.Context, parent []string, name string) error {
	return c.createItem(parent, name, func(itemDir string) error {
		return writeMessage(filepath.Join(itemDir, folderFileName), &grpcviewstorev1.Folder{
			Meta: &grpcviewstorev1.ItemMeta{Name: name},
		})
	})
}

// CreateRequest creates a request named name (with the given service/method and
// an empty body) inside the folder addressed by parent.
func (c *Collection) CreateRequest(_ context.Context, parent []string, name, service, method string) error {
	return c.createItem(parent, name, func(itemDir string) error {
		return writeMessage(filepath.Join(itemDir, requestFileName), &grpcviewstorev1.Request{
			Meta:    &grpcviewstorev1.ItemMeta{Name: name},
			Service: service,
			Method:  method,
		})
	})
}

// createItem is the shared body of CreateFolder/CreateRequest: it resolves the
// parent, rejects a duplicate display name, allocates a unique slug + directory,
// calls write to lay down the item's files, and records the slug in the parent's
// order.
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

// UpdateRequest applies a partial update to the request named name inside parent.
// Only the files a patch actually touches are rewritten.
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

	if patch.Name == nil && patch.Service == nil && patch.Method == nil && patch.DraftBody == nil && patch.DraftMetadata == nil && patch.DraftMetadataScript == nil && !patch.SetMiddleware && !patch.SetTarget {
		return nil
	}
	// Reuse the request.json readChildren already decoded (ch.request) rather
	// than re-opening and re-decoding the same file.
	p := filepath.Join(itemDir, requestFileName)
	dr := ch.request
	// Rename: same slug-identity model as Move/renameMeta — only meta.name
	// changes; the slug/dir is stable so open tabs/history keyed by slug survive.
	// A collision with a different sibling is rejected (ErrAlreadyExists); a
	// no-op rename to the current name is skipped so it doesn't self-collide.
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
	if patch.DraftBody != nil {
		dr.DraftBody = *patch.DraftBody
	}
	if patch.DraftMetadata != nil {
		dr.DraftMetadata = patch.DraftMetadata // Struct: identical on both sides
	}
	if patch.DraftMetadataScript != nil {
		dr.DraftMetadataScript = *patch.DraftMetadataScript // plain string, like DraftBody
	}
	// SetMiddleware gates the repeated middleware list (it has no nil "unset"):
	// replace it (nil/empty clears; protojson then omits it).
	if patch.SetMiddleware {
		dr.Middleware = patch.Middleware
	}
	// SetTarget gates the per-request target message (no nil "unset"): replace it
	// (a nil Server clears it, so protojson omits it and it reads back as the
	// reflection default).
	if patch.SetTarget {
		dr.Target = serverToTarget(patch.Target)
	}
	return writeMessage(p, dr)
}

// RequestMiddleware returns the ordered attached-middleware display names for the
// request named name inside parent — the cheap read the invoke path's middleware
// step needs, without loading the whole tree (mirroring Sources/Scripts). It
// returns ErrItemNotFound when there is no such request (an ad-hoc or just-deleted
// target), which the invoke path treats as "no middleware".
func (c *Collection) RequestMiddleware(_ context.Context, parent []string, name string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return nil, err
	}
	parentDir, err := c.resolveFolder(parent)
	if err != nil {
		return nil, err
	}
	present, err := c.readChildren(parentDir)
	if err != nil {
		return nil, err
	}
	ch, ok := findByName(present, name)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrItemNotFound, name)
	}
	if ch.kind != kindRequest {
		return nil, fmt.Errorf("%w: %q", ErrNotARequest, name)
	}
	return ch.request.GetMiddleware(), nil
}

// AppendHistory records one completed invoke in the run history of the request
// named name inside parent, retaining the newest max entries (max <= 0 keeps all)
// and logging when older entries are dropped. Run history is gitignored local
// state (storage.md §4): it lives under .grpcview/history/ keyed by the request's
// stable slug path — so it survives a rename and never hits git — and is loaded
// back into Request.history on Load. A missing target request returns
// ErrItemNotFound; Invoke persists history best-effort and does not fail on it.
func (c *Collection) AppendHistory(_ context.Context, parent []string, name string, entry *grpcviewv1.History, max int) error {
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

// Delete removes the item named name from parent. It is idempotent: deleting a
// missing item is a no-op (matching the previous blob behavior).
func (c *Collection) Delete(_ context.Context, parent []string, name string) error {
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

// Move relocates and/or renames an item. from and to are full display-name
// paths including the item name as the last segment.
//
//   - Same parent, different last segment: a rename. Per the slug-identity model
//     this only edits the config's meta.name; the slug/dir is stable so any
//     references survive.
//   - Different parent: the directory is moved (keeping its slug when free in the
//     destination) and, if the last segment differs, its meta.name is updated.
func (c *Collection) Move(_ context.Context, from, to []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	if len(from) == 0 || len(to) == 0 {
		return fmt.Errorf("%w: empty path", ErrInvalidMove)
	}
	srcParent, srcName := from[:len(from)-1], from[len(from)-1]
	dstParent, dstName := to[:len(to)-1], to[len(to)-1]

	srcParentDir, err := c.resolveFolder(srcParent)
	if err != nil {
		return err
	}
	srcPresent, err := c.readChildren(srcParentDir)
	if err != nil {
		return err
	}
	srcCh, ok := findByName(srcPresent, srcName)
	if !ok {
		return fmt.Errorf("%w: %q", ErrItemNotFound, srcName)
	}
	srcItemDir := filepath.Join(srcParentDir, srcCh.slug)

	dstParentDir, err := c.resolveFolder(dstParent)
	if err != nil {
		return err
	}

	// Same parent -> pure rename (edit meta.name; the slug/dir stays stable).
	if srcParentDir == dstParentDir {
		if dstName == srcName {
			return nil
		}
		if _, exists := findByName(srcPresent, dstName); exists {
			return fmt.Errorf("%w: %q", ErrAlreadyExists, dstName)
		}
		return c.renameMeta(srcItemDir, srcCh.kind, dstName)
	}

	// Cross-folder move: can't move a folder into itself or a descendant.
	if dstParentDir == srcItemDir || strings.HasPrefix(dstParentDir, srcItemDir+string(filepath.Separator)) {
		return fmt.Errorf("%w: cannot move an item into itself", ErrInvalidMove)
	}

	dstPresent, err := c.readChildren(dstParentDir)
	if err != nil {
		return err
	}
	if _, exists := findByName(dstPresent, dstName); exists {
		return fmt.Errorf("%w: %q", ErrAlreadyExists, dstName)
	}

	// Keep the slug (stable identity) if free in the destination; otherwise
	// derive a fresh unique one from the new name.
	dstUsed := slugSet(dstPresent)
	dstSlug := srcCh.slug
	if dstUsed[strings.ToLower(dstSlug)] || isReserved(dstSlug) {
		dstSlug = uniqueSlug(dstName, dstUsed)
	}

	srcBase, err := c.reconciledSlugsFrom(srcParentDir, srcPresent)
	if err != nil {
		return err
	}
	dstBase, err := c.reconciledSlugsFrom(dstParentDir, dstPresent)
	if err != nil {
		return err
	}

	dstItemDir := filepath.Join(dstParentDir, dstSlug)
	if err := os.Rename(srcItemDir, dstItemDir); err != nil {
		return fmt.Errorf("move %s -> %s: %w", srcItemDir, dstItemDir, err)
	}
	if dstName != srcName {
		if err := c.renameMeta(dstItemDir, srcCh.kind, dstName); err != nil {
			return err
		}
	}
	if err := c.writeOrder(srcParentDir, slices.DeleteFunc(srcBase, func(s string) bool { return s == srcCh.slug })); err != nil {
		return err
	}
	return c.writeOrder(dstParentDir, append(dstBase, dstSlug))
}

// PutDescriptorState persists the committed descriptor sources (grpcview.json)
// and the resolved-schema cache (gitignored .grpcview/cache/services.json). The
// derived, merged descriptorSet is cached alongside the services (same wire
// Workspace carrier), never committed.
func (c *Collection) PutDescriptorState(_ context.Context, sources []*grpcviewv1.DescriptorSource, services []*grpcviewv1.Service, descriptorSet []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	if err := c.writeServicesCache(services, descriptorSet); err != nil {
		return err
	}
	col, err := c.readCollection()
	if err != nil {
		return err
	}
	diskSources, err := wireToDiskSources(sources)
	if err != nil {
		return err
	}
	col.Sources = diskSources
	return c.writeCollection(col)
}

// migrateLegacyBlob detects a legacy single-blob workspace file at the
// collection path and materializes it into the new directory tree, backing up
// the original as <name>.blob.bak. It runs at most once per collection: Store.Open
// calls it on every RPC, but migrateOnce collapses all but the first to a cached
// result. Once the path is a directory (migrated or fresh) it is a no-op.
func (c *Collection) migrateLegacyBlob(_ context.Context) error {
	c.migrateOnce.Do(func() { c.migrateErr = c.migrate() })
	return c.migrateErr
}

func (c *Collection) migrate() error {
	info, err := os.Stat(c.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil // new collection; nothing to migrate
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return nil // already a directory tree
	}

	data, err := os.ReadFile(c.root)
	if err != nil {
		return fmt.Errorf("read legacy blob: %w", err)
	}
	ws := &grpcviewv1.Workspace{}
	if err := proto.Unmarshal(data, ws); err != nil {
		return fmt.Errorf("unmarshal legacy blob %s: %w", c.root, err)
	}

	backup := c.root + ".blob.bak"
	if err := os.Rename(c.root, backup); err != nil {
		return fmt.Errorf("back up legacy blob: %w", err)
	}
	if err := c.writeWorkspace(ws); err != nil {
		return fmt.Errorf("materialize migrated tree: %w", err)
	}
	c.logger.Info("migrated legacy blob to directory tree", "backup", backup)
	return nil
}

// writeWorkspace materializes a complete (wire) Workspace to disk. Used by
// migration.
func (c *Collection) writeWorkspace(ws *grpcviewv1.Workspace) error {
	if err := os.MkdirAll(c.treeRoot(), 0o755); err != nil {
		return err
	}
	if err := c.ensureGitignore(); err != nil {
		return err
	}
	rootSlugs, err := c.writeItems(c.treeRoot(), ws.GetItem().GetFolder().GetItems())
	if err != nil {
		return err
	}
	if len(ws.GetServices()) > 0 {
		if err := c.writeServicesCache(ws.GetServices(), ws.GetDescriptorSet()); err != nil {
			return err
		}
	}
	sources, err := wireToDiskSources(ws.GetSources())
	if err != nil {
		return err
	}
	return c.writeCollection(&grpcviewstorev1.Collection{
		Name:    ws.GetName(),
		Items:   rootSlugs,
		Sources: sources,
	})
}

// writeItems writes each wire item as a directory under dir and returns their
// slugs in order. Folders recurse (writing their own folder.json); requests get
// request.json.
func (c *Collection) writeItems(dir string, items []*grpcviewv1.Item) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	used := make(map[string]bool, len(items))
	slugs := make([]string, 0, len(items))
	for _, item := range items {
		slug := uniqueSlug(item.GetName(), used)
		used[strings.ToLower(slug)] = true
		slugs = append(slugs, slug)

		itemDir := filepath.Join(dir, slug)
		if err := os.MkdirAll(itemDir, 0o755); err != nil {
			return nil, err
		}
		switch content := item.GetContent().(type) {
		case *grpcviewv1.Item_Folder:
			childSlugs, err := c.writeItems(itemDir, content.Folder.GetItems())
			if err != nil {
				return nil, err
			}
			if err := writeMessage(filepath.Join(itemDir, folderFileName), &grpcviewstorev1.Folder{
				Meta:  &grpcviewstorev1.ItemMeta{Name: item.GetName()},
				Items: childSlugs,
			}); err != nil {
				return nil, err
			}
		case *grpcviewv1.Item_Request:
			if err := writeMessage(filepath.Join(itemDir, requestFileName), wireToDiskRequest(item.GetName(), content.Request)); err != nil {
				return nil, err
			}
		}
	}
	return slugs, nil
}

// renameMeta edits only the display name in an item's config, leaving the slug
// (directory name) untouched.
func (c *Collection) renameMeta(itemDir string, kind itemKind, newName string) error {
	switch kind {
	case kindFolder:
		p := filepath.Join(itemDir, folderFileName)
		ff := &grpcviewstorev1.Folder{}
		if err := readMessage(p, ff); err != nil {
			return err
		}
		if ff.Meta == nil {
			ff.Meta = &grpcviewstorev1.ItemMeta{}
		}
		ff.Meta.Name = newName
		return writeMessage(p, ff)
	case kindRequest:
		p := filepath.Join(itemDir, requestFileName)
		rf := &grpcviewstorev1.Request{}
		if err := readMessage(p, rf); err != nil {
			return err
		}
		if rf.Meta == nil {
			rf.Meta = &grpcviewstorev1.ItemMeta{}
		}
		rf.Meta.Name = newName
		return writeMessage(p, rf)
	}
	return fmt.Errorf("unknown item kind")
}

// resolveFolder walks the display-name path from the tree root and returns the
// addressed folder's on-disk directory. Every segment must be a folder.
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

// readChildren classifies the immediate item subdirectories of dir (those
// containing a folder.json or request.json), reading each display name.
// Non-item entries, hidden/state dirs, and reserved names are skipped.
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
		case fileExists(filepath.Join(sub, requestFileName)):
			rf := &grpcviewstorev1.Request{}
			if err := readMessage(filepath.Join(sub, requestFileName), rf); err != nil {
				return nil, err
			}
			children = append(children, childEntry{slug: slug, name: cmp.Or(rf.GetMeta().GetName(), slug), kind: kindRequest, request: rf})
		}
	}
	return children, nil
}

// reconcile orders present against the parent's recorded slug list and logs any
// listed-but-absent slugs that get dropped (see reconcileOrder).
func (c *Collection) reconcile(dir string, listed []string, present []childEntry) []childEntry {
	ordered, dropped := reconcileOrder(listed, present)
	for _, slug := range dropped {
		c.logger.Warn("dropping ordered item missing on disk", "dir", dir, "slug", slug)
	}
	return ordered
}

// walkFolder assembles the ordered child (wire) Items of the folder at dir.
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

// readItem builds the (wire) Item for a single classified child, reusing the
// config readChildren already decoded (ch.folder/ch.request) instead of
// re-reading it.
func (c *Collection) readItem(parentDir string, ch childEntry) (*grpcviewv1.Item, error) {
	dir := filepath.Join(parentDir, ch.slug)
	switch ch.kind {
	case kindFolder:
		children, err := c.walkFolder(dir, ch.folder.GetItems())
		if err != nil {
			return nil, err
		}
		return &grpcviewv1.Item{
			Name:    ch.name,
			Content: &grpcviewv1.Item_Folder{Folder: &grpcviewv1.Folder{Items: children}},
		}, nil
	case kindRequest:
		req := diskToWireRequest(ch.name, ch.request)
		req.History = c.readHistory(dir) // gitignored sidecar; nil when absent
		return &grpcviewv1.Item{
			Name:    ch.name,
			Content: &grpcviewv1.Item_Request{Request: req},
		}, nil
	}
	return nil, fmt.Errorf("unknown item kind")
}

// readOrder returns the recorded child-slug order for the folder at dir (from
// grpcview.json for the tree root, folder.json otherwise).
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

// writeOrder rewrites the child-slug order for the folder at dir, preserving the
// folder's other config (e.g. meta.name).
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

// reconciledSlugsFrom returns dir's recorded order reconciled against the given
// present-children snapshot (no disk re-read), so a caller that has already
// listed the directory can update the order without racing its own new writes.
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
	return col, nil
}

func (c *Collection) writeCollection(col *grpcviewstorev1.Collection) error {
	if col.SchemaVersion == 0 {
		col.SchemaVersion = schemaVersion
	}
	if col.Name == "" {
		col.Name = c.name
	}
	return writeMessage(c.collectionFilePath(), col)
}

func (c *Collection) ensureGitignore() error {
	p := filepath.Join(c.root, gitignoreFileName)
	if fileExists(p) {
		return nil
	}
	content := "# grpcview local state — run history, resolved-schema cache, secrets, UI state\n" + stateDir + "/\n"
	return writeFileAtomic(p, []byte(content), 0o644)
}

// writeServicesCache persists the resolved schema to the gitignored state dir.
// The cache is a snapshot of the wire services (a genuine 1:1), so it reuses the
// wire message rather than a disk-specific one; being gitignored and regenerable,
// it is not part of the committed on-disk schema.
func (c *Collection) writeServicesCache(services []*grpcviewv1.Service, descriptorSet []byte) error {
	if err := os.MkdirAll(filepath.Dir(c.servicesCachePath()), 0o755); err != nil {
		return err
	}
	wrapper := &grpcviewv1.Workspace{Services: services, DescriptorSet: descriptorSet}
	data, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(wrapper)
	if err != nil {
		return fmt.Errorf("marshal services cache: %w", err)
	}
	return writeFileAtomic(c.servicesCachePath(), append(data, '\n'), 0o644)
}

func (c *Collection) readServicesCache() ([]*grpcviewv1.Service, []byte, error) {
	data, err := os.ReadFile(c.servicesCachePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	wrapper := &grpcviewv1.Workspace{}
	if err := protojson.Unmarshal(data, wrapper); err != nil {
		return nil, nil, fmt.Errorf("unmarshal services cache: %w", err)
	}
	return wrapper.GetServices(), wrapper.GetDescriptorSet(), nil
}

// historyFilePath returns the run-history file for the request whose on-disk
// directory is itemDir. History is keyed by the request's tree-relative slug path
// under .grpcview/history/ (e.g. tree/users/get-user -> history/users/get-user/
// history.json), so it stays attached across a rename (the slug is stable) and
// out of the committed tree.
func (c *Collection) historyFilePath(itemDir string) (string, error) {
	rel, err := filepath.Rel(c.treeRoot(), itemDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.historyRoot(), rel, historyFileName), nil
}

// readHistory returns the persisted run history for the request at itemDir, or
// nil if there is none. A path or decode error is logged and swallowed (returning
// nil) so a corrupt local history file can never block loading the workspace —
// history is regenerable local state, not committed data.
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

// historyMarshal renders the history file without emitting default values (unlike
// managed committed files): a successful run's OK (code 0) status and a stream's
// empty response bytes then cost nothing on disk.
var historyMarshal = protojson.MarshalOptions{Multiline: true, Indent: "  "}

// readHistoryFile / writeHistoryFile reuse the wire Request as a carrier for its
// repeated History — mirroring how the services cache reuses the wire Workspace.
// The payload is a genuine snapshot of wire messages, so no disk-specific schema
// is needed, and being gitignored/regenerable it is not part of the committed
// on-disk format.
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

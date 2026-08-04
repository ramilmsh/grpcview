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
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// Load assembles the whole collection as a wire Collection.
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
	sources, err := diskToWireSources(col.GetSources())
	if err != nil {
		return nil, err
	}
	merged, err := c.readMergedCache()
	if err != nil {
		return nil, err
	}
	// The manifest owns the source list and its order; the cache only adds each
	// source's derived summary, so it is overlaid by id.
	overlayResolved(sources, merged.GetSources())
	scripts, err := c.loadScripts(col.GetScripts())
	if err != nil {
		return nil, err
	}

	name := cmp.Or(col.GetName(), c.defaultName())
	return &grpcviewv1.Collection{
		Name: name,
		Item: &grpcviewv1.Item{
			Name:    name,
			Content: &grpcviewv1.Item_Folder{Folder: &grpcviewv1.Folder{Items: rootItems}},
		},
		Sources:       sources,
		Services:      merged.GetServices(),
		Scripts:       scripts,
		DescriptorSet: merged.GetDescriptorSet(),
	}, nil
}

func overlayResolved(sources, cached []*grpcviewv1.DescriptorSource) {
	byID := make(map[string]*grpcviewv1.DescriptorSource, len(cached))
	for _, s := range cached {
		byID[s.GetId()] = s
	}
	for _, s := range sources {
		if c, ok := byID[s.GetId()]; ok {
			s.Resolved = c.GetResolved()
		}
	}
}

// Merged returns the resolved-schema cache as a bare Collection: the merged descriptor set, the
// services derived from it, and each source's derived summary. It reads one file and never walks
// the request tree, so the invoke path can consult the definitions without paying for a Load.
// An absent cache is not an error — it yields a zero Collection, which reads as "no definitions".
func (c *Collection) Merged(_ context.Context) (*grpcviewv1.Collection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readMergedCache()
}

// Sources returns just the committed descriptor sources from the manifest.
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

// Services returns the resolved-schema cache's services; an absent cache is not an error.
func (c *Collection) Services(_ context.Context) ([]*grpcviewv1.Service, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	merged, err := c.readMergedCache()
	return merged.GetServices(), err
}

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

// Create creates a new, empty collection (manifest, tree/) at this address, with the
// manifest's display name set to name — or, when name is empty, to the collection
// directory's own base name. A collection that already exists here is ErrAlreadyExists,
// wrapped with this collection's id: unlike the old EnsureCreated this replaces, a typo'd
// address must not silently materialize a collection or silently reuse one.
func (c *Collection) Create(_ context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fileExists(c.collectionFilePath()) {
		// Its own sentinel, not ErrAlreadyExists: that one reads "item already exists" and
		// is about tree items, and it maps to FailedPrecondition where this wants AlreadyExists.
		return fmt.Errorf("%w: %q", ErrCollectionExists, c.id)
	}
	if err := os.MkdirAll(c.treeRoot(), 0o755); err != nil {
		return err
	}
	if name == "" {
		name = c.defaultName()
	}
	return c.writeCollection(&grpcviewstorev1.Collection{Name: name})
}

// CreateFolder creates a folder inside the folder addressed by the display-name path parent.
func (c *Collection) CreateFolder(_ context.Context, parent []string, name string) error {
	return c.createItem(parent, name, func(itemDir string) error {
		return writeMessage(filepath.Join(itemDir, folderFileName), &grpcviewstorev1.Folder{
			Meta: &grpcviewstorev1.ItemMeta{Name: name},
		})
	})
}

// CreateRequest creates a request with an empty body inside the folder addressed by parent.
func (c *Collection) CreateRequest(_ context.Context, parent []string, name, service, method string) error {
	return c.createItem(parent, name, func(itemDir string) error {
		return writeMessage(filepath.Join(itemDir, requestFileName), &grpcviewstorev1.Request{
			Meta:    &grpcviewstorev1.ItemMeta{Name: name},
			Service: service,
			Method:  method,
		})
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
	p := filepath.Join(itemDir, requestFileName)
	dr := ch.request
	// A rename rewrites only meta.name; the slug/dir is stable, so slug-keyed state
	// (tabs, history) survives.
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
	if patch.DraftMetadataScript != nil {
		dr.DraftMetadataScript = *patch.DraftMetadataScript
	}
	if patch.SetMiddleware {
		dr.Middleware = patch.Middleware
	}
	if patch.SetTarget {
		dr.Target = serverToTarget(patch.Target)
	}
	return writeMessage(p, dr)
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

// FolderMetadataChain returns the ancestor folder metadata scripts (root→leaf) for
// the node whose PARENT-folder path is path; path excludes the node's own name.
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

// resolveChild's callers must hold c.mu and have already called c.ensureExists.
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

func (c *Collection) ResolveRequest(_ context.Context, parent []string, name string) (*grpcviewv1.Request, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return nil, err
	}
	_, ch, err := c.resolveRequestChild(parent, name)
	if err != nil {
		return nil, err
	}
	return diskToWireRequest(ch.name, ch.request), nil
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

// AppendHistory records one completed invoke, keeping the newest max entries (max <= 0 keeps all).
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

// Delete is idempotent: deleting a missing item is a no-op.
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

// Move places the item named name in parent into newParent (empty = the tree root)
// ahead of the sibling named before there; a nil or no-longer-present before appends.
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
	// filepath.Rel, not a string prefix: ".../foo" is a prefix of ".../foobar", a legal sibling.
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
	// A slug is unique only WITHIN a folder, so a reparent must allocate a fresh one.
	newSlug := uniqueSlug(ch.name, slugSet(destChildren))

	srcBase, err := c.reconciledSlugsFrom(parentDir, present)
	if err != nil {
		return err
	}
	destBase, err := c.reconciledSlugsFrom(destDir, destChildren)
	if err != nil {
		return err
	}
	// The directory must move FIRST: "moved, order not" is the half-applied state
	// reconcileOrder self-heals on the next load.
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

// DescriptorState is a collection's whole descriptor configuration.
type DescriptorState struct {
	// Sources is in PRIORITY order (earlier wins).
	Sources []*grpcviewv1.DescriptorSource
	// Keyed by source id; an absent id keeps whatever the manifest already stores.
	Uploads map[string]*descriptorpb.FileDescriptorSet
	// Keyed by source id; an absent id leaves the existing cache entry alone.
	Resolves map[string]*grpcviewstorev1.ResolvedSource

	Services      []*grpcviewv1.Service
	DescriptorSet []byte
}

// PutDescriptorState writes the whole state under one lock — so a reader never sees a
// source list and a merged view that disagree — and prunes caches of dropped sources.
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
	existing := make(map[string]*descriptorpb.FileDescriptorSet, len(col.GetSources()))
	for _, ds := range col.GetSources() {
		if up := ds.GetUpload(); up != nil {
			existing[ds.GetId()] = up.GetDescriptorSet()
		}
	}
	diskSources, err := wireToDiskSources(state.Sources, func(id string) *descriptorpb.FileDescriptorSet {
		if fds, ok := state.Uploads[id]; ok {
			return fds
		}
		return existing[id]
	})
	if err != nil {
		return err
	}
	col.Sources = diskSources
	if err := c.writeCollection(col); err != nil {
		return err
	}

	for _, r := range state.Resolves {
		if err := c.writeSourceResolve(r); err != nil {
			return err
		}
	}
	ids := make([]string, 0, len(state.Sources))
	for _, s := range state.Sources {
		ids = append(ids, s.GetId())
	}
	if err := c.pruneSourceResolves(ids); err != nil {
		return err
	}
	return c.writeMergedCache(state.Sources, state.Services, state.DescriptorSet)
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
			Content: &grpcviewv1.Item_Folder{Folder: &grpcviewv1.Folder{
				Items:               children,
				DraftMetadataScript: ch.folder.GetDraftMetadataScript(),
			}},
		}, nil
	case kindRequest:
		req := diskToWireRequest(ch.name, ch.request)
		req.History = c.readHistory(dir)
		return &grpcviewv1.Item{
			Name:    ch.name,
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

// reconciledSlugsFrom uses an already-read children snapshot, so a caller cannot race its own writes.
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

func (c *Collection) writeMergedCache(sources []*grpcviewv1.DescriptorSource, services []*grpcviewv1.Service, descriptorSet []byte) error {
	if err := os.MkdirAll(filepath.Dir(c.servicesCachePath()), 0o755); err != nil {
		return err
	}
	wrapper := &grpcviewv1.Collection{Sources: sources, Services: services, DescriptorSet: descriptorSet}
	data, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(wrapper)
	if err != nil {
		return fmt.Errorf("marshal services cache: %w", err)
	}
	return writeFileAtomic(c.servicesCachePath(), append(data, '\n'), 0o644)
}

func (c *Collection) readMergedCache() (*grpcviewv1.Collection, error) {
	wrapper := &grpcviewv1.Collection{}
	data, err := os.ReadFile(c.servicesCachePath())
	if errors.Is(err, os.ErrNotExist) {
		return wrapper, nil
	}
	if err != nil {
		return nil, err
	}
	if err := protojson.Unmarshal(data, wrapper); err != nil {
		return nil, fmt.Errorf("unmarshal services cache: %w", err)
	}
	return wrapper, nil
}

// SourceResolves reads every cached per-source resolve, keyed by source id.
func (c *Collection) SourceResolves(_ context.Context) (map[string]*grpcviewstorev1.ResolvedSource, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := os.ReadDir(c.sourcesCacheRoot())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]*grpcviewstorev1.ResolvedSource{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make(map[string]*grpcviewstorev1.ResolvedSource, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), sourceCacheFileExt) {
			continue
		}
		path := filepath.Join(c.sourcesCacheRoot(), e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		r := &grpcviewstorev1.ResolvedSource{}
		if err := proto.Unmarshal(data, r); err != nil {
			c.logger.Warn("dropping unreadable source cache entry", "file", e.Name(), "error", err)
			_ = os.Remove(path)
			continue
		}
		out[r.GetId()] = r
	}
	return out, nil
}

// writeSourceResolve uses binary proto: nothing diffs this cache, and protojson would cost far more.
func (c *Collection) writeSourceResolve(r *grpcviewstorev1.ResolvedSource) error {
	if err := os.MkdirAll(c.sourcesCacheRoot(), 0o755); err != nil {
		return err
	}
	data, err := proto.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal source resolve %s: %w", r.GetId(), err)
	}
	return writeFileAtomic(c.sourceCachePath(r.GetId()), data, 0o644)
}

func (c *Collection) pruneSourceResolves(keep []string) error {
	wanted := make(map[string]bool, len(keep))
	for _, id := range keep {
		wanted[filepath.Base(c.sourceCachePath(id))] = true
	}
	entries, err := os.ReadDir(c.sourcesCacheRoot())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || wanted[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(c.sourcesCacheRoot(), e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// historyFilePath keys history by tree-relative SLUG path, so it survives a rename and stays out of git.
func (c *Collection) historyFilePath(itemDir string) (string, error) {
	rel, err := filepath.Rel(c.treeRoot(), itemDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.historyRoot(), rel, historyFileName), nil
}

// readHistory swallows errors: a corrupt local history file must never block a load.
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

// historyMarshal omits default values, unlike the managed committed files.
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

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

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// Scripts live in scripts/<slug>/script.json, a committed directory sibling of tree/
// (scripting.md's threat model assumes scripts ship with the collection). They are a
// flat, ordered list — the parent's order is Collection.scripts — so they reuse the
// request machinery's slug/rename/reconcile helpers (slugify, uniqueSlug,
// reconcileOrder) but not the tree's folder-path resolution.

// scriptEntry is a classified on-disk script directory: its stable slug (dir name),
// display name (from meta), and the decoded config (reused by loadScripts so it need
// not re-open the file). It satisfies slugNamed so it shares reconcileOrder with tree
// items.
type scriptEntry struct {
	slug   string
	name   string
	script *grpcviewstorev1.Script
}

func (s scriptEntry) orderSlug() string { return s.slug }
func (s scriptEntry) orderName() string { return s.name }

// CreateScript creates a new, empty script of the given kind. Its name must be unique
// among scripts.
func (c *Collection) CreateScript(_ context.Context, name string, kind grpcviewv1.ScriptKind) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	present, err := c.readScripts()
	if err != nil {
		return err
	}
	if _, ok := findScript(present, name); ok {
		return fmt.Errorf("%w: %q", ErrAlreadyExists, name)
	}

	slug := uniqueSlug(name, scriptSlugSet(present))
	base, err := c.reconciledScriptSlugs(present)
	if err != nil {
		return err
	}
	scriptDir := filepath.Join(c.scriptsRoot(), slug)
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		return err
	}
	if err := writeMessage(filepath.Join(scriptDir, scriptFileName), &grpcviewstorev1.Script{
		Meta: &grpcviewstorev1.ItemMeta{Name: name},
		Kind: wireToDiskScriptKind(kind),
	}); err != nil {
		return err
	}
	return c.writeScriptOrder(append(base, slug))
}

// UpdateScript applies a partial update to the script named name (source and/or a
// rename). Only script.json is rewritten. A rename follows the slug-identity model
// (meta.name only; the slug/dir is stable) and rejects a collision with a different
// script; a no-op rename to the current name is skipped so it doesn't self-collide.
func (c *Collection) UpdateScript(_ context.Context, name string, patch ScriptPatch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	present, err := c.readScripts()
	if err != nil {
		return err
	}
	se, ok := findScript(present, name)
	if !ok {
		return fmt.Errorf("%w: %q", ErrItemNotFound, name)
	}
	if patch.Name == nil && patch.Source == nil {
		return nil
	}

	sf := se.script // the config readScripts already decoded
	if patch.Name != nil && *patch.Name != name {
		if _, exists := findScript(present, *patch.Name); exists {
			return fmt.Errorf("%w: %q", ErrAlreadyExists, *patch.Name)
		}
		if sf.Meta == nil {
			sf.Meta = &grpcviewstorev1.ItemMeta{}
		}
		sf.Meta.Name = *patch.Name
	}
	if patch.Source != nil {
		sf.Source = *patch.Source
	}
	return writeMessage(filepath.Join(c.scriptsRoot(), se.slug, scriptFileName), sf)
}

// DeleteScript removes the script named name. Idempotent: deleting a missing script is
// a no-op (matching Delete for tree items).
func (c *Collection) DeleteScript(_ context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	present, err := c.readScripts()
	if err != nil {
		return err
	}
	se, ok := findScript(present, name)
	if !ok {
		return nil
	}
	base, err := c.reconciledScriptSlugs(present)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(c.scriptsRoot(), se.slug)); err != nil {
		return err
	}
	return c.writeScriptOrder(slices.DeleteFunc(base, func(s string) bool { return s == se.slug }))
}

// loadScripts assembles the ordered wire Scripts for Load, ordering the present
// scripts against the manifest's recorded slug list (listed).
func (c *Collection) loadScripts(listed []string) ([]*grpcviewv1.Script, error) {
	present, err := c.readScripts()
	if err != nil {
		return nil, err
	}
	if len(present) == 0 {
		return nil, nil
	}
	ordered := c.reconcileScripts(listed, present)
	out := make([]*grpcviewv1.Script, 0, len(ordered))
	for _, se := range ordered {
		out = append(out, diskToWireScript(se.name, se.script))
	}
	return out, nil
}

// readScripts classifies the script subdirectories of scripts/ (those containing a
// script.json), decoding each. Non-script and hidden entries are skipped.
func (c *Collection) readScripts() ([]scriptEntry, error) {
	entries, err := os.ReadDir(c.scriptsRoot())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var scripts []scriptEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		if strings.HasPrefix(slug, ".") {
			continue
		}
		p := filepath.Join(c.scriptsRoot(), slug, scriptFileName)
		if !fileExists(p) {
			continue
		}
		sf := &grpcviewstorev1.Script{}
		if err := readMessage(p, sf); err != nil {
			return nil, err
		}
		scripts = append(scripts, scriptEntry{slug: slug, name: cmp.Or(sf.GetMeta().GetName(), slug), script: sf})
	}
	return scripts, nil
}

// reconcileScripts orders present against the manifest's recorded slug list and logs
// any listed-but-absent slugs that get dropped (see reconcileOrder).
func (c *Collection) reconcileScripts(listed []string, present []scriptEntry) []scriptEntry {
	ordered, dropped := reconcileOrder(listed, present)
	for _, slug := range dropped {
		c.logger.Warn("dropping ordered script missing on disk", "slug", slug)
	}
	return ordered
}

// reconciledScriptSlugs returns the manifest's script order reconciled against the
// given present-scripts snapshot (no disk re-read), so a caller that already listed
// the directory can update the order without racing its own new writes.
func (c *Collection) reconciledScriptSlugs(present []scriptEntry) ([]string, error) {
	col, err := c.readCollection()
	if err != nil {
		return nil, err
	}
	ordered := c.reconcileScripts(col.GetScripts(), present)
	slugs := make([]string, len(ordered))
	for i, se := range ordered {
		slugs[i] = se.slug
	}
	return slugs, nil
}

// writeScriptOrder rewrites the manifest's script-slug order, preserving the rest of
// the collection config.
func (c *Collection) writeScriptOrder(slugs []string) error {
	if slugs == nil {
		slugs = []string{}
	}
	col, err := c.readCollection()
	if err != nil {
		return err
	}
	col.Scripts = slugs
	return c.writeCollection(col)
}

// findScript returns the script with the given display name (first match).
func findScript(present []scriptEntry, name string) (scriptEntry, bool) {
	for _, se := range present {
		if se.name == name {
			return se, true
		}
	}
	return scriptEntry{}, false
}

// scriptSlugSet returns the lowercased slugs of the given scripts, for uniqueSlug.
func scriptSlugSet(present []scriptEntry) map[string]bool {
	set := make(map[string]bool, len(present))
	for _, se := range present {
		set[strings.ToLower(se.slug)] = true
	}
	return set
}

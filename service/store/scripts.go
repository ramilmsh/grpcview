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

type scriptEntry struct {
	slug   string
	name   string
	script *grpcviewstorev1.Script
}

func (s scriptEntry) orderSlug() string { return s.slug }
func (s scriptEntry) orderName() string { return s.name }

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

	sf := se.script
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

func (c *Collection) reconcileScripts(listed []string, present []scriptEntry) []scriptEntry {
	ordered, dropped := reconcileOrder(listed, present)
	for _, slug := range dropped {
		c.logger.Warn("dropping ordered script missing on disk", "slug", slug)
	}
	return ordered
}

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

func findScript(present []scriptEntry, name string) (scriptEntry, bool) {
	for _, se := range present {
		if se.name == name {
			return se, true
		}
	}
	return scriptEntry{}, false
}

func scriptSlugSet(present []scriptEntry) map[string]bool {
	set := make(map[string]bool, len(present))
	for _, se := range present {
		set[strings.ToLower(se.slug)] = true
	}
	return set
}

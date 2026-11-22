package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

func (c *Collection) readScripts() ([]*grpcviewv1.Script, error) {
	root := c.scriptsRoot()
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	var scripts []*grpcviewv1.Script
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || filepath.Ext(d.Name()) != ".ts" {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		scripts = append(scripts, &grpcviewv1.Script{
			Path:   "scripts/" + filepath.ToSlash(rel),
			Source: string(data),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(scripts, func(a, b *grpcviewv1.Script) int {
		return cmp.Compare(a.GetPath(), b.GetPath())
	})
	return scripts, nil
}

func (c *Collection) CreateScript(_ context.Context, scriptPath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	abs, err := c.resolveScriptPath(scriptPath)
	if err != nil {
		return err
	}
	if fileExists(abs) {
		return fmt.Errorf("%w: %q", ErrAlreadyExists, scriptPath)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return writeSourceFile(abs, "")
}

func (c *Collection) UpdateScript(_ context.Context, scriptPath string, patch ScriptPatch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	abs, err := c.resolveScriptPath(scriptPath)
	if err != nil {
		return err
	}
	if !fileExists(abs) {
		return fmt.Errorf("%w: %q", ErrItemNotFound, scriptPath)
	}
	if patch.Source == nil && patch.NewPath == nil {
		return nil
	}

	if patch.Source != nil {
		if err := writeSourceFile(abs, *patch.Source); err != nil {
			return err
		}
	}
	if patch.NewPath != nil {
		newAbs, err := c.resolveScriptPath(*patch.NewPath)
		if err != nil {
			return err
		}
		if fileExists(newAbs) {
			return fmt.Errorf("%w: %q", ErrAlreadyExists, *patch.NewPath)
		}
		if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
			return err
		}
		if err := os.Rename(abs, newAbs); err != nil {
			return err
		}
	}
	return nil
}

func (c *Collection) DeleteScript(_ context.Context, scriptPath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureExists(); err != nil {
		return err
	}
	abs, err := c.resolveScriptPath(scriptPath)
	if err != nil {
		return err
	}
	if !fileExists(abs) {
		return nil
	}
	if err := os.Remove(abs); err != nil {
		return err
	}
	return c.pruneEmptyScriptDirs(filepath.Dir(abs))
}

// dir is resolveScriptPath output, so it is compared against the resolved root.
func (c *Collection) pruneEmptyScriptDirs(dir string) error {
	root, err := wsroot.Confine(c.scriptsRoot(), ".")
	if err != nil {
		return err
	}
	for dir != root && wsroot.Within(root, dir) {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return nil
		}
		if err := os.Remove(dir); err != nil {
			return err
		}
		dir = filepath.Dir(dir)
	}
	return nil
}

// resolveScriptPath validates a collection-relative script path and returns its
// symlink-resolved location, confined to the collection. Used by CreateScript, UpdateScript
// (both path and new_path) and DeleteScript.
func (c *Collection) resolveScriptPath(p string) (string, error) {
	invalid := fmt.Errorf("script path must be under scripts/ and end in .ts, got %q", p)
	if p == "" || filepath.IsAbs(p) || !strings.HasSuffix(p, ".ts") {
		return "", invalid
	}
	slash := filepath.ToSlash(p)
	for _, seg := range strings.Split(slash, "/") {
		if seg == ".." {
			return "", invalid
		}
	}
	clean := path.Clean(slash)
	if segments := strings.Split(clean, "/"); segments[0] != "scripts" {
		return "", invalid
	}
	real, err := wsroot.Confine(c.root, filepath.FromSlash(clean))
	if err != nil {
		return "", fmt.Errorf("script path %q: %w", p, err)
	}
	return real, nil
}

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
	return os.WriteFile(abs, nil, 0o644)
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
		if err := os.WriteFile(abs, []byte(*patch.Source), 0o644); err != nil {
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

func (c *Collection) pruneEmptyScriptDirs(dir string) error {
	root := c.scriptsRoot()
	for dir != root {
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

// resolveScriptPath validates a collection-relative script path and returns its absolute
// location. Used by CreateScript, UpdateScript (both path and new_path) and DeleteScript.
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
	abs := filepath.Join(c.root, filepath.FromSlash(clean))
	if !withinDir(c.scriptsRoot(), abs) {
		return "", invalid
	}
	return abs, nil
}

// withinDir mirrors service/scripting/bundler.go's containment guard.
func withinDir(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

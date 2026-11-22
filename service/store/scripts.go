package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
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
		// Pruning a dot-directory only skips what the classifier rejects.
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(c.root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		abs, err := c.resolveFile(rel, PathScript)
		if err != nil {
			return nil
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		scripts = append(scripts, &grpcviewv1.Script{
			Path:   rel,
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
	defer c.lockWrite()()
	if err := c.ensureExists(); err != nil {
		return err
	}
	abs, err := c.resolveFile(scriptPath, PathScript)
	if err != nil {
		return err
	}
	if fileExists(abs) {
		return fmt.Errorf("%w: %q", ErrAlreadyExists, scriptPath)
	}
	if err := c.tx.mkdirAll(filepath.Dir(abs)); err != nil {
		return err
	}
	return c.tx.writeSource(abs, "")
}

func (c *Collection) UpdateScript(_ context.Context, scriptPath string, patch ScriptPatch) error {
	defer c.lockWrite()()
	if err := c.ensureExists(); err != nil {
		return err
	}
	abs, err := c.resolveFile(scriptPath, PathScript)
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
		if err := c.tx.writeSource(abs, *patch.Source); err != nil {
			return err
		}
	}
	if patch.NewPath != nil {
		newAbs, err := c.resolveFile(*patch.NewPath, PathScript)
		if err != nil {
			return err
		}
		if fileExists(newAbs) {
			return fmt.Errorf("%w: %q", ErrAlreadyExists, *patch.NewPath)
		}
		if err := c.tx.mkdirAll(filepath.Dir(newAbs)); err != nil {
			return err
		}
		if err := c.tx.rename(abs, newAbs); err != nil {
			return err
		}
	}
	return nil
}

func (c *Collection) DeleteScript(_ context.Context, scriptPath string) error {
	defer c.lockWrite()()
	if err := c.ensureExists(); err != nil {
		return err
	}
	abs, err := c.resolveFile(scriptPath, PathScript)
	if err != nil {
		return err
	}
	if !fileExists(abs) {
		return nil
	}
	if err := c.tx.remove(abs); err != nil {
		return err
	}
	return c.pruneEmptyScriptDirs(filepath.Dir(abs))
}

func (c *Collection) pruneEmptyScriptDirs(dir string) error {
	for ; dir != c.scriptsRoot(); dir = filepath.Dir(dir) {
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
		if err := c.tx.remove(dir); err != nil {
			return err
		}
	}
	return nil
}

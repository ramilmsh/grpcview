package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

const (
	maxWorkspaceModuleFiles    = 1000
	maxWorkspaceModuleBytes    = 8 << 20 // 8 MB, whichever of this or the file count hits first
	maxWorkspaceModuleFileSize = 256 << 10
)

func (w Workspace) ListWorkspaceModules(_ context.Context, _ *connect.Request[grpcviewv1.ListWorkspaceModulesRequest]) (*connect.Response[grpcviewv1.ListWorkspaceModulesResponse], error) {
	modules, err := scanWorkspaceModules(w.root)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to scan the workspace for TypeScript modules: %w", err))
	}
	return connect.NewResponse(&grpcviewv1.ListWorkspaceModulesResponse{Modules: modules}), nil
}

var errWorkspaceModuleCapHit = errors.New("workspace module cap hit")

// A workspace root is often a whole repository (this one contains ui/, hundreds of .ts files and a
// node_modules), so the walk is bounded on every axis: skipped directories, skipped files, and a total
// cap that stops the walk outright rather than silently thinning the result — a truncated listing that
// looked complete would read as "the module doesn't exist" in the editor, not as "the cap hit".
func scanWorkspaceModules(root string) ([]*grpcviewv1.WorkspaceModule, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absRoot = filepath.Clean(absRoot)

	var (
		modules    []*grpcviewv1.WorkspaceModule
		totalBytes int
		oversized  []string
		cutoff     string
	)

	walkErr := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == absRoot {
				return err
			}
			slog.Default().Debug("skip path during workspace module scan", "path", path, "error", err)
			return nil
		}

		isRoot := path == absRoot
		name := d.Name()
		if d.IsDir() {
			if isRoot {
				return nil
			}
			if strings.HasPrefix(name, ".") || name == store.NodeModulesDirName || strings.HasPrefix(name, store.BazelSymlinkPrefix) {
				return fs.SkipDir
			}
			// A directory declaring its own package.json is a JS/TS project of its own (ui/, most
			// notably), not grpcview script material. The workspace root is exempt: a repo root
			// often has one.
			if _, err := os.Stat(filepath.Join(path, "package.json")); err == nil {
				return fs.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".d.ts") {
			return nil
		}
		if name == store.BodyFileName || name == store.MetadataFileName {
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), store.RequestFileName)); err == nil {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			slog.Default().Debug("skip file during workspace module scan", "path", path, "error", err)
			return nil
		}
		if info.Size() > maxWorkspaceModuleFileSize {
			oversized = append(oversized, path)
			return nil
		}

		if len(modules) >= maxWorkspaceModuleFiles || totalBytes+int(info.Size()) > maxWorkspaceModuleBytes {
			cutoff = path
			return errWorkspaceModuleCapHit
		}

		content, err := os.ReadFile(path)
		if err != nil {
			slog.Default().Debug("skip file during workspace module scan", "path", path, "error", err)
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}
		modules = append(modules, &grpcviewv1.WorkspaceModule{
			Path:    filepath.ToSlash(rel),
			Content: string(content),
		})
		totalBytes += len(content)
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errWorkspaceModuleCapHit) {
		return nil, walkErr
	}

	if cutoff != "" {
		slog.Default().Warn("workspace module scan hit its cap and stopped early; some TypeScript files were dropped",
			"root", absRoot, "listed", len(modules), "max_files", maxWorkspaceModuleFiles,
			"max_bytes", maxWorkspaceModuleBytes, "stopped_at", cutoff)
	}
	for _, path := range oversized {
		slog.Default().Warn("workspace module skipped: over the per-file size cap",
			"path", path, "max_bytes", maxWorkspaceModuleFileSize)
	}

	slices.SortFunc(modules, func(a, b *grpcviewv1.WorkspaceModule) int {
		return strings.Compare(a.GetPath(), b.GetPath())
	})
	return modules, nil
}

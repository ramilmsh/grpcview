package store

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/bazelbuild"
)

// A bazel label goes in verbatim and is ASSUMED canonical: whoever accepts one from a human
// canonicalizes it there, so two spellings of one target cannot become two sources.
func SourceID(src *grpcviewv1.DescriptorSource) string {
	switch s := src.GetSource().(type) {
	case *grpcviewv1.DescriptorSource_Reflection:
		return reflectionSourceID(addressTLSFromServer(s.Reflection))
	case *grpcviewv1.DescriptorSource_Upload:
		return uploadSourceID(s.Upload.GetFileName())
	case *grpcviewv1.DescriptorSource_Bazel:
		return bazelSourceID(s.Bazel.GetLabel())
	case *grpcviewv1.DescriptorSource_Protos:
		return protosSourceID(s.Protos.GetPath())
	default:
		return ""
	}
}

func diskSourceID(ds *grpcviewstorev1.DescriptorSource) string {
	switch s := ds.GetSource().(type) {
	case *grpcviewstorev1.DescriptorSource_Reflection:
		return reflectionSourceID(s.Reflection.GetAddress(), s.Reflection.GetTls())
	case *grpcviewstorev1.DescriptorSource_Upload:
		return uploadSourceID(s.Upload.GetFileName())
	case *grpcviewstorev1.DescriptorSource_Bazel:
		return bazelSourceID(s.Bazel.GetLabel())
	case *grpcviewstorev1.DescriptorSource_Protos:
		return protosSourceID(s.Protos.GetPath())
	default:
		return ""
	}
}

func reflectionSourceID(address string, tls bool) string {
	if tls {
		return "reflection:" + address + "+tls"
	}
	return "reflection:" + address
}

func uploadSourceID(fileName string) string { return "upload:" + fileName }

func bazelSourceID(label string) string { return "bazel:" + label }

func protosSourceID(path string) string { return "protos:" + path }

// Rewrites manifest-authored config IN PLACE so the next write persists it, and runs everywhere an id
// is derived from disk — the OTHER door config comes through. `sources add` canonicalizes before
// deriving an id; a hand-written grpcview.json does not, so "bazel://pkg" vs "bazel://pkg:pkg", or
// "./api" vs "api", would be one thing as two sources. What will not canonicalize keeps its raw
// spelling, so the row stays visible with its resolve error on it.
func canonicalizeSource(ds *grpcviewstorev1.DescriptorSource, root string, logger *slog.Logger) {
	switch s := ds.GetSource().(type) {
	case *grpcviewstorev1.DescriptorSource_Bazel:
		if s.Bazel.GetLabel() == "" {
			return
		}
		canon, err := bazelbuild.CanonicalLabel(s.Bazel.GetLabel())
		if err != nil {
			logger.Warn("keeping a bazel label that will not canonicalize as it stands",
				"label", s.Bazel.GetLabel(), "error", err)
			return
		}
		s.Bazel.Label = canon
	case *grpcviewstorev1.DescriptorSource_Protos:
		s.Protos.Path = canonicalProtosPath(root, s.Protos.GetPath())
		for i, ip := range s.Protos.GetImportPaths() {
			s.Protos.ImportPaths[i] = canonicalProtosPath(root, ip)
		}
	}
}

// The root-relative, cleaned, slash-separated spelling `sources add` records. Lexical only: symlinks
// are resolve-time confinement's business. A path outside root stays as authored.
func canonicalProtosPath(root, p string) string {
	if p == "" {
		return p
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return p
	}
	abs := filepath.FromSlash(p)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(absRoot, abs)
	}
	rel, err := filepath.Rel(absRoot, filepath.Clean(abs))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return filepath.ToSlash(rel)
}

func normalizeSources(
	in []*grpcviewstorev1.DescriptorSource,
	root string,
	logger *slog.Logger,
) []*grpcviewstorev1.DescriptorSource {
	if len(in) == 0 {
		return in
	}
	out := make([]*grpcviewstorev1.DescriptorSource, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, ds := range in {
		canonicalizeSource(ds, root, logger)
		if ds.GetId() == "" {
			ds.Id = diskSourceID(ds)
		}
		if ds.GetId() == "" {
			logger.Warn("dropping a descriptor source with neither config nor an id from the manifest")
			continue
		}
		if seen[ds.GetId()] {
			logger.Warn("dropping a duplicate descriptor source from the manifest", "id", ds.GetId())
			continue
		}
		seen[ds.GetId()] = true
		out = append(out, ds)
	}
	return out
}

func (s *Store) readWorkspaceManifest() (*grpcviewstorev1.Workspace, error) {
	ws := &grpcviewstorev1.Workspace{}
	err := readMessage(filepath.Join(s.root, WorkspaceFileName), ws)
	if errors.Is(err, os.ErrNotExist) {
		return ws, nil
	}
	if err != nil {
		return nil, err
	}
	return ws, nil
}

func (s *Store) WorkspaceBazel() (*grpcviewstorev1.BazelConfig, error) {
	ws, err := s.readWorkspaceManifest()
	if err != nil {
		return nil, err
	}
	return ws.GetBazel(), nil
}

func (s *Store) workspaceDefinitions() (map[string]*grpcviewstorev1.DescriptorSource, error) {
	ws, err := s.readWorkspaceManifest()
	if err != nil {
		return nil, err
	}
	return s.definitionSet(ws.GetSources()), nil
}

func (s *Store) definitionSet(in []*grpcviewstorev1.DescriptorSource) map[string]*grpcviewstorev1.DescriptorSource {
	defs := make(map[string]*grpcviewstorev1.DescriptorSource, len(in))
	for _, ds := range in {
		canonicalizeSource(ds, s.root, s.logger)
		if ds.GetId() == "" {
			ds.Id = diskSourceID(ds)
		}
		switch {
		case ds.GetId() == "":
			s.logger.Warn("ignoring a workspace definition with neither config nor an id", "manifest", WorkspaceFileName)
			continue
		case ds.GetUpload() != nil:
			s.logger.Warn("ignoring a workspace definition that is an upload: an upload's bytes belong to the collection that supplied them",
				"id", ds.GetId(), "manifest", WorkspaceFileName)
			continue
		case defs[ds.GetId()] != nil:
			s.logger.Warn("ignoring a duplicate workspace definition", "id", ds.GetId(), "manifest", WorkspaceFileName)
			continue
		}
		if ds.GetCommitDescriptors() {
			s.logger.Warn("ignoring commit_descriptors on a workspace definition: it belongs to a collection's own entry",
				"id", ds.GetId(), "manifest", WorkspaceFileName)
			ds.CommitDescriptors = false
		}
		defs[ds.GetId()] = ds
	}
	return defs
}

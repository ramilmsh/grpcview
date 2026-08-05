package store

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// SourceID derives a source's stable identity from its config, so re-adding it refreshes in place.
func SourceID(src *grpcviewv1.DescriptorSource) string {
	switch s := src.GetSource().(type) {
	case *grpcviewv1.DescriptorSource_Reflection:
		return reflectionSourceID(addressTLSFromServer(s.Reflection))
	case *grpcviewv1.DescriptorSource_Upload:
		return uploadSourceID(s.Upload.GetFileName())
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
	default:
		return ""
	}
}

// The +tls suffix keeps the plaintext and TLS views of one address distinct sources.
func reflectionSourceID(address string, tls bool) string {
	if tls {
		return "reflection:" + address + "+tls"
	}
	return "reflection:" + address
}

func uploadSourceID(fileName string) string { return "upload:" + fileName }

// normalizeSources runs on every manifest read: it fills in missing ids and drops entries that
// cannot be addressed at all.
//
// An entry carrying config gets its id derived from that config. An entry carrying an id and NO
// config is a REFERENCE to a workspace-level definition, and it is kept exactly as it stands: the
// bare form IS the disk shape of a reference, and merging the definition in happens where the wire
// list is produced (diskToWireSource) rather than here, so a round trip through this function
// cannot inline shared config into a collection. Only an entry with neither config nor an id is
// dropped — there is nothing to derive and nothing to look up.
func normalizeSources(
	in []*grpcviewstorev1.DescriptorSource,
	logger *slog.Logger,
) []*grpcviewstorev1.DescriptorSource {
	if len(in) == 0 {
		return in
	}
	out := make([]*grpcviewstorev1.DescriptorSource, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, ds := range in {
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

// readWorkspaceManifest is the ONE reader of grpcview.work.json, shared by discovery and by the
// shared-definition lookup so a single place knows the file exists.
//
// The file is OPTIONAL, so absent is an empty manifest rather than an error: a repo nobody has
// pinned anything in declares no collections and no shared definitions, which is exactly what the
// zero message says. A file that will not PARSE is still an error — that is a broken commit, not a
// zero-config workspace.
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

// workspaceDefinitions is the shared definition set a collection's references resolve against,
// keyed by source id.
//
// Read on demand, with no cache: it is one small protojson file, every caller is already doing
// file I/O on the collection manifest next to it, and a cache would need an invalidation story for
// a file the user edits in an editor rather than through any RPC.
func (s *Store) workspaceDefinitions() (map[string]*grpcviewstorev1.DescriptorSource, error) {
	ws, err := s.readWorkspaceManifest()
	if err != nil {
		return nil, err
	}
	return s.definitionSet(ws.GetSources()), nil
}

// definitionSet validates the manifest's definitions into a set keyed by id. Every rule is
// enforced with a WARNING and a skip rather than a failure: this is committed config a colleague
// may have written, and one bad entry must not stop a whole workspace loading. A skipped
// definition simply is not there, which a referencing collection reports as a reference nothing
// defines.
func (s *Store) definitionSet(in []*grpcviewstorev1.DescriptorSource) map[string]*grpcviewstorev1.DescriptorSource {
	defs := make(map[string]*grpcviewstorev1.DescriptorSource, len(in))
	for _, ds := range in {
		if ds.GetId() == "" {
			ds.Id = diskSourceID(ds)
		}
		switch {
		case ds.GetId() == "":
			s.logger.Warn("ignoring a workspace definition with neither config nor an id", "manifest", WorkspaceFileName)
			continue
		case ds.GetUpload() != nil:
			// An upload has no pointer to re-acquire from, and its bytes are owned by the
			// collection that supplied them — a workspace-level upload has nowhere to live.
			s.logger.Warn("ignoring a workspace definition that is an upload: an upload's bytes belong to the collection that supplied them",
				"id", ds.GetId(), "manifest", WorkspaceFileName)
			continue
		case defs[ds.GetId()] != nil:
			s.logger.Warn("ignoring a duplicate workspace definition", "id", ds.GetId(), "manifest", WorkspaceFileName)
			continue
		}
		if ds.GetCommitDescriptors() {
			// Where a resolve is STORED is per collection — a sidecar lives inside one — so the
			// flag says nothing about a shared definition, and each referencing collection's own
			// entry carries its own.
			s.logger.Warn("ignoring commit_descriptors on a workspace definition: it belongs to a collection's own entry",
				"id", ds.GetId(), "manifest", WorkspaceFileName)
			ds.CommitDescriptors = false
		}
		defs[ds.GetId()] = ds
	}
	return defs
}

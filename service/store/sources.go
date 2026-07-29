package store

import (
	"log/slog"
	"strconv"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// This file owns descriptor-source IDENTITY. An id is what every id-keyed
// operation (refresh, remove, reorder) addresses a source by, and what keys its
// resolve cache, so there is exactly one definition of the format here — used by
// both the wire and the on-disk shape — plus the migration that gives a manifest
// written before ids existed the identity those operations now require.

// legacyUploadFileName names an upload recovered from a pre-id manifest, which
// stored no name. It matches what the old UI called such a source, so a migrated
// row keeps the label it had.
const legacyUploadFileName = "uploaded bytes"

// SourceID derives a descriptor source's stable identity from its config: the
// dial target for reflection, the file name for an upload. Identity is
// config-derived rather than random so re-adding the same source is recognized as
// the same source and refreshes in place — which is also why an upload is keyed by
// file name and not by a hash of its bytes: rebuilding an image must refresh the
// source it came from, not create a second one.
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

// diskSourceID is SourceID over the on-disk shape, so a manifest can be given
// identities without first converting it for the wire.
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

// reflectionSourceID and uploadSourceID are the id format itself — the only place
// it is spelled out. The +tls suffix keeps the plaintext and TLS views of one
// address distinct sources, since they can serve different schemas.
func reflectionSourceID(address string, tls bool) string {
	if tls {
		return "reflection:" + address + "+tls"
	}
	return "reflection:" + address
}

func uploadSourceID(fileName string) string { return "upload:" + fileName }

// normalizeSources brings a manifest's descriptor sources up to the current
// on-disk schema and returns the cleaned list. It runs on every manifest read, so
// identity is guaranteed for every caller downstream — and the next write persists
// the result, making the migration a one-time rewrite rather than a permanent
// read-time fixup.
//
//   - A legacy upload (bytes stored directly on the source, no name) becomes an
//     Upload. Without this the descriptors would be dropped as an unknown field,
//     and the manifest is their only committed copy — the resolve cache is
//     gitignored.
//   - A source with no id gets the one derived from its config.
//   - A duplicate (same derived identity — which the pre-id add could append,
//     since it had no notion of "the same source") is dropped, keeping the
//     higher-priority copy. That is the same collapse re-adding a source performs
//     now, and it matters beyond tidiness: two rows with one id make every
//     id-keyed operation ambiguous.
//   - An entry with no content at all is dropped. It can never resolve, and with
//     an empty id it would swallow operations aimed at other rows.
func normalizeSources(
	in []*grpcviewstorev1.DescriptorSource,
	logger *slog.Logger,
) []*grpcviewstorev1.DescriptorSource {
	if len(in) == 0 {
		return in
	}
	out := make([]*grpcviewstorev1.DescriptorSource, 0, len(in))
	seen := make(map[string]bool, len(in))
	legacy := 0
	for _, ds := range in {
		if fds := ds.GetLegacyDescriptorSet(); fds != nil && ds.GetUpload() == nil {
			legacy++
			name := legacyUploadFileName
			if legacy > 1 {
				// A pre-id manifest could hold several unnamed uploads; number the
				// later ones so each keeps its own identity and label.
				name = legacyUploadFileName + " " + strconv.Itoa(legacy)
			}
			ds.Source = &grpcviewstorev1.DescriptorSource_Upload{
				Upload: &grpcviewstorev1.Upload{FileName: name, DescriptorSet: fds},
			}
		}
		// Read-only migration field: never carried forward.
		ds.LegacyDescriptorSet = nil

		if ds.GetId() == "" {
			ds.Id = diskSourceID(ds)
		}
		if ds.GetId() == "" {
			logger.Warn("dropping a descriptor source with no content from the manifest")
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

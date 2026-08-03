package store

import (
	"log/slog"

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

// normalizeSources runs on every manifest read: it fills in missing ids and drops duplicate or
// contentless entries.
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

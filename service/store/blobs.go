package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
)

// The descriptor store: content-addressed blobs holding what each source resolved to, plus one
// per-collection index pointing source ids at digests.
//
// The blobs live under the WORKSPACE state root rather than a collection's, because that is the
// whole point of addressing them by content: five collections in a monorepo pointing at one
// schema hold one copy of its bytes. The index is per collection because priority order and
// which sources are configured are per collection.
//
// Nothing here stores the merged view (services, the merged descriptor set, per-source
// summaries). That is a pure function of these blobs plus the manifest's source order, and it is
// derived in memory on first touch — see service/workspace/definitions.go.

func (s *Store) blobsRoot() string { return filepath.Join(s.stateRoot, blobsDir) }

func (s *Store) blobPath(digest string) string {
	return filepath.Join(s.blobsRoot(), digest+blobFileExt)
}

// normalizeDescriptorSet re-encodes fds and returns the canonical bytes with their digest.
//
// The round trip through a fresh message with DiscardUnknown is what makes the digest a function
// of the SCHEMA rather than of the encoder that produced it: a `buf build` image carries
// buf-specific extension fields that ride along in a parsed message's unknown fields, and
// without dropping them the same schema arriving from two producers would hash two ways and
// occupy two blobs. That normalization used to live in the upload parser; it belongs at the
// store's write boundary, where it applies to every kind of source.
//
// Deterministic marshalling for the same reason: the digest is the file name, so the encoding
// must depend on the message's contents and nothing else.
func normalizeDescriptorSet(fds *descriptorpb.FileDescriptorSet) ([]byte, string, error) {
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(fds)
	if err != nil {
		return nil, "", err
	}
	clean := &descriptorpb.FileDescriptorSet{}
	if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, clean); err != nil {
		return nil, "", err
	}
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(clean)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:]), nil
}

// putBlob stores one resolved descriptor set and returns its digest. The write is
// write-if-absent: identical content is never rewritten, so a reorder — which re-resolves
// nothing — touches no file at all, and nothing downstream sees an mtime move that does not
// correspond to a content change.
//
// Binary proto, not protojson: nothing reads or diffs a blob, and protojson of a
// multi-megabyte descriptor set costs several times as much to write and parse.
//
// Callers must hold s.blobMu.
func (s *Store) putBlob(fds *descriptorpb.FileDescriptorSet) (string, error) {
	canonical, digest, err := normalizeDescriptorSet(fds)
	if err != nil {
		return "", err
	}
	path := s.blobPath(digest)
	if fileExists(path) {
		return digest, nil
	}
	if err := os.MkdirAll(s.blobsRoot(), 0o755); err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, canonical, 0o644); err != nil {
		return "", err
	}
	return digest, nil
}

// readBlob returns the descriptor set a digest names. A blob that is absent, or that will not
// parse, is (nil, nil): the caller treats the source as unresolved and a refresh fixes it. An
// unparseable blob is also DELETED, because write-if-absent would otherwise make the corruption
// permanent — the next resolve of the same schema would find a file at that digest and skip the
// write forever.
func (s *Store) readBlob(digest string) (*descriptorpb.FileDescriptorSet, error) {
	path := s.blobPath(digest)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(data, fds); err != nil {
		s.logger.Warn("dropping unreadable descriptor blob", "digest", digest, "error", err)
		_ = os.Remove(path)
		return nil, nil
	}
	return fds, nil
}

// gcBlobs deletes every blob no collection in this workspace references.
//
// It is workspace-scoped because the blobs are: pruning only the ids the writing collection
// dropped would delete bytes another collection is pointing at. The reference set is read from
// every collection state directory's index — including indexes whose collection is no longer on
// disk, which deliberately keeps their blobs alive. Detecting a dead collection means resolving
// a hashed state directory back to a path that by definition is not there, and the cost of
// being wrong is asymmetric: a stale blob is bytes, a wrongly-deleted one is a re-dial of a
// target that may be unreachable.
//
// Callers must hold s.blobMu, which is what makes the scan safe: every writer takes it before
// writing a blob and holds it until its index names that blob, so no live blob is ever
// unreferenced while this runs.
func (s *Store) gcBlobs() error {
	entries, err := os.ReadDir(s.blobsRoot())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	referenced, err := s.referencedDigests()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), blobFileExt) {
			continue
		}
		if referenced[strings.TrimSuffix(e.Name(), blobFileExt)] {
			continue
		}
		if err := os.Remove(filepath.Join(s.blobsRoot(), e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store) referencedDigests() (map[string]bool, error) {
	stateDirs, err := os.ReadDir(filepath.Join(s.stateRoot, collectionsStateSubdir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	referenced := make(map[string]bool)
	for _, dir := range stateDirs {
		if !dir.IsDir() {
			continue
		}
		path := filepath.Join(s.stateRoot, collectionsStateSubdir, dir.Name(), descriptorIndexFileName)
		index := &grpcviewstorev1.DescriptorIndex{}
		err := readMessage(path, index)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			// An index nobody can parse has already lost its collection's pointers, so its
			// blobs are unreachable whatever we do here; refusing to collect would let one
			// corrupt file block every future write in the workspace instead.
			s.logger.Warn("ignoring unreadable descriptor index while collecting blobs", "path", path, "error", err)
			continue
		}
		for _, e := range index.GetEntries() {
			referenced[e.GetDigest()] = true
		}
	}
	return referenced, nil
}

func (c *Collection) descriptorIndexPath() string {
	return filepath.Join(c.state, descriptorIndexFileName)
}

// readDescriptorIndex is keyed by source id. An absent index is an empty one — a collection
// whose sources have never resolved — and an unparseable one is treated the same way, since
// every entry in it is reconstructible by re-resolving.
func (c *Collection) readDescriptorIndex() (map[string]*grpcviewstorev1.DescriptorIndexEntry, error) {
	index := &grpcviewstorev1.DescriptorIndex{}
	err := readMessage(c.descriptorIndexPath(), index)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]*grpcviewstorev1.DescriptorIndexEntry{}, nil
	}
	if err != nil {
		c.logger.Warn("dropping unreadable descriptor index", "path", c.descriptorIndexPath(), "error", err)
		return map[string]*grpcviewstorev1.DescriptorIndexEntry{}, nil
	}
	byID := make(map[string]*grpcviewstorev1.DescriptorIndexEntry, len(index.GetEntries()))
	for _, e := range index.GetEntries() {
		byID[e.GetSourceId()] = e
	}
	return byID, nil
}

func (c *Collection) writeDescriptorIndex(entries []*grpcviewstorev1.DescriptorIndexEntry) error {
	if err := os.MkdirAll(c.state, 0o755); err != nil {
		return err
	}
	return writeMessage(c.descriptorIndexPath(), &grpcviewstorev1.DescriptorIndex{
		SchemaVersion: schemaVersion,
		Entries:       entries,
	})
}

// DescriptorBlobs returns what each of this collection's sources last resolved to, keyed by
// source id: the index entry and the blob its digest names, assembled. A source with no entry —
// or whose blob has gone missing — is simply absent from the map, which reads as "not resolved
// yet"; that is never an error, because a load must survive a half-collected state root and a
// refresh repairs it.
func (c *Collection) DescriptorBlobs(_ context.Context) (map[string]*grpcviewstorev1.ResolvedSource, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	index, err := c.readDescriptorIndex()
	if err != nil {
		return nil, err
	}
	out := make(map[string]*grpcviewstorev1.ResolvedSource, len(index))
	for id, entry := range index {
		fds, err := c.store.readBlob(entry.GetDigest())
		if err != nil {
			return nil, err
		}
		if fds == nil {
			c.logger.Warn("descriptor blob missing for source", "source", id, "digest", entry.GetDigest())
			continue
		}
		out[id] = &grpcviewstorev1.ResolvedSource{
			Id:            id,
			DescriptorSet: fds,
			ServiceNames:  entry.GetServiceNames(),
		}
	}
	return out, nil
}

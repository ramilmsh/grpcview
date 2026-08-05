package store

import (
	"bytes"
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

// The descriptor store: what each source last resolved to, in one of two locations chosen by
// that source's commit_descriptors flag.
//
// Off (the default) is a content-addressed blob plus one per-collection index pointing source
// ids at digests. The blobs live under the WORKSPACE state root rather than a collection's,
// because that is the whole point of addressing them by content: five collections in a monorepo
// pointing at one schema hold one copy of its bytes. The index is per collection because
// priority order and which sources are configured are per collection.
//
// On is a protojson sidecar inside the collection, named by SOURCE ID. The asymmetry is
// deliberate and the reasons are opposite: content-addressing is right for a cache (dedup,
// immutability), while a digest-named committed file would turn every refresh into an add plus
// a delete instead of a diff, which is the whole readable-protojson rationale for committing.
//
// Nothing here stores the merged view (services, the merged descriptor set, per-source
// summaries). That is a pure function of these resolves plus the manifest's source order, and
// it is derived in memory on first touch — see service/workspace/definitions.go.

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

func (c *Collection) descriptorSidecarsRoot() string {
	return filepath.Join(c.root, descriptorsDir)
}

// descriptorSidecarPath names a committed sidecar after the SOURCE ID — hashedName, so two ids
// that slugify alike never share a file — and never after the content's digest, which would make
// every refresh a rename and cost the readable diff committing is for.
func (c *Collection) descriptorSidecarPath(id string) string {
	return filepath.Join(c.descriptorSidecarsRoot(), hashedName(id)+descriptorSidecarExt)
}

// writeDescriptorSidecar commits one resolve as protojson, through the same codec every other
// committed file uses so it is formatted and diffed like them.
//
// The descriptor set goes through normalizeDescriptorSet's canonical form first, for the same
// reason the digest does and with a harder requirement: refreshing twice against an unchanged
// upstream must leave `git status` clean, so the bytes must depend on the schema and not on
// which encoder — a `buf build` image, a reflection round trip — produced this copy of it.
//
// service_names is committed alongside the descriptors because it is not derivable: a reflection
// server's ListServices is authoritative and narrower than "every service these files define".
// That is also what makes a sidecar self-sufficient in a fresh clone with no local state.
//
// Like putBlob the write is skipped when the bytes already on disk match, so a mutation that
// re-resolved nothing — a reorder, a remove, a refresh against an unchanged upstream — moves no
// mtime on a file that can be megabytes.
func (c *Collection) writeDescriptorSidecar(r *grpcviewstorev1.ResolvedSource) error {
	canonical, _, err := normalizeDescriptorSet(r.GetDescriptorSet())
	if err != nil {
		return err
	}
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(canonical, fds); err != nil {
		return err
	}
	data, err := marshalMessage(&grpcviewstorev1.ResolvedSource{
		Id:            r.GetId(),
		DescriptorSet: fds,
		ServiceNames:  r.GetServiceNames(),
	})
	if err != nil {
		return err
	}
	path := c.descriptorSidecarPath(r.GetId())
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err := os.MkdirAll(c.descriptorSidecarsRoot(), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o644)
}

// readDescriptorSidecar returns the resolve a source's committed sidecar holds, or nil when
// there is none. An unparseable sidecar is nil too, with a warning: the source reads as
// unresolved and a refresh rewrites the file, which is strictly better than failing every load
// of a collection because someone hand-edited a committed descriptor set.
func (c *Collection) readDescriptorSidecar(id string) (*grpcviewstorev1.ResolvedSource, error) {
	path := c.descriptorSidecarPath(id)
	r := &grpcviewstorev1.ResolvedSource{}
	err := readMessage(path, r)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		c.logger.Warn("ignoring unreadable committed descriptor sidecar", "path", path, "error", err)
		return nil, nil
	}
	r.Id = id
	return r, nil
}

// pruneDescriptorSidecars deletes every sidecar in the committed directory that keep — the file
// names of the sources that just wrote one — does not name, so a source that left the list or
// had its flag turned off loses its file. It is per collection, unlike gcBlobs, because a
// sidecar belongs to exactly one; and the directory itself goes when it empties, so a collection
// that commits no descriptors has no empty directory sitting in git.
func (c *Collection) pruneDescriptorSidecars(keep map[string]bool) error {
	root := c.descriptorSidecarsRoot()
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	remaining := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), descriptorSidecarExt) || keep[e.Name()] {
			remaining++
			continue
		}
		if err := os.Remove(filepath.Join(root, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if remaining > 0 {
		return nil
	}
	if err := os.Remove(root); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// blobResolve assembles the uncommitted form of one source's resolve: the index entry plus the
// blob its digest names. A missing blob is (nil, nil) — the source reads as unresolved and a
// refresh repairs it — because a load must survive a half-collected state root.
func (c *Collection) blobResolve(id string, entry *grpcviewstorev1.DescriptorIndexEntry) (*grpcviewstorev1.ResolvedSource, error) {
	fds, err := c.store.readBlob(entry.GetDigest())
	if err != nil {
		return nil, err
	}
	if fds == nil {
		c.logger.Warn("descriptor blob missing for source", "source", id, "digest", entry.GetDigest())
		return nil, nil
	}
	return &grpcviewstorev1.ResolvedSource{
		Id:            id,
		DescriptorSet: fds,
		ServiceNames:  entry.GetServiceNames(),
	}, nil
}

// DescriptorResolves returns what each of this collection's sources last resolved to, keyed by
// source id, from whichever location holds it — a committed sidecar for a source whose flag is
// on, the index entry plus its blob otherwise. A source with neither is simply absent from the
// map, which reads as "not resolved yet" and is never an error.
//
// The sidecar half is what makes a fresh clone work with no refresh at all, which is the whole
// reason the flag exists: the committed file is read straight out of the collection, and the
// empty state root a clone has costs nothing.
func (c *Collection) DescriptorResolves(_ context.Context) (map[string]*grpcviewstorev1.ResolvedSource, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	index, err := c.readDescriptorIndex()
	if err != nil {
		return nil, err
	}
	out := make(map[string]*grpcviewstorev1.ResolvedSource, len(index))
	for id, entry := range index {
		r, err := c.blobResolve(id, entry)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out[id] = r
		}
	}

	// The manifest is what says which sources committed their descriptors, so the sidecars can
	// only be found through it. A collection with no readable manifest has no sources at all,
	// and answering from the index alone is then exactly right.
	col, err := c.readCollection()
	if err != nil {
		return out, nil
	}
	for _, ds := range col.GetSources() {
		if !ds.GetCommitDescriptors() {
			continue
		}
		r, err := c.readDescriptorSidecar(ds.GetId())
		if err != nil {
			return nil, err
		}
		if r != nil {
			out[ds.GetId()] = r
		}
	}
	return out, nil
}

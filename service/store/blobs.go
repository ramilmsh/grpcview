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

func (s *Store) blobsRoot() string { return filepath.Join(s.stateRoot, blobsDir) }

func (s *Store) blobPath(digest string) string {
	return filepath.Join(s.blobsRoot(), digest+blobFileExt)
}

// Re-encoded through a fresh message with DiscardUnknown so the digest is a function of the SCHEMA,
// not of the encoder: a `buf build` image carries extension fields that ride along as unknown fields,
// and the same schema from two producers would otherwise hash two ways. Deterministic marshalling for
// the same reason — the digest is the file name.
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

// Write-if-absent: identical content is never rewritten, so a reorder touches no file and no mtime
// moves without a content change. Callers must hold s.blobMu.
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

// An absent or unparseable blob is (nil, nil): the source reads as unresolved and a refresh fixes it.
// An unparseable one is also DELETED, because write-if-absent would otherwise make the corruption
// permanent. Callers must hold s.blobMu.
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

// Deletes every blob no collection in this workspace references. Workspace-scoped because the blobs
// are: pruning only the writing collection's dropped ids would delete bytes another collection points
// at. Indexes whose collection is gone deliberately keep their blobs alive — a stale blob is bytes, a
// wrongly-deleted one is a re-dial of a target that may be unreachable.
//
// Callers must hold s.blobMu, which is what makes the scan safe: every writer holds it from writing a
// blob until its index names that blob, so no live blob is ever unreferenced while this runs.
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

// Named after the SOURCE ID, never the content digest: a digest-named committed file would make every
// refresh an add plus a delete instead of the readable diff committing is for.
func (c *Collection) descriptorSidecarPath(id string) string {
	return filepath.Join(c.descriptorSidecarsRoot(), hashedName(id)+descriptorSidecarExt)
}

// Normalized bytes, and skipped when they match what is on disk: refreshing twice against an unchanged
// upstream must leave `git status` clean. service_names is committed alongside because it is not
// derivable, which is also what makes a sidecar self-sufficient in a fresh clone.
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

// An unparseable sidecar is nil, not an error: the source reads as unresolved and a refresh rewrites
// it, rather than failing every load because someone hand-edited a committed descriptor set.
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

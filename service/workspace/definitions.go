package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync"

	"connectrpc.com/connect"

	"github.com/jhump/protoreflect/desc"
	"google.golang.org/protobuf/proto"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

type definitions struct {
	services      map[string]*desc.ServiceDescriptor
	sources       []*grpcviewv1.DescriptorSource
	serviceList   []*grpcviewv1.Service
	descriptorSet []byte
	mergeErr      error
	listDigest    [sha256.Size]byte
}

// Identifies a source list by its whole authored content, with the derived Resolved summaries excluded
// because they are an output of the merge rather than an input. Writer invalidation cannot be the
// memo's only coherency mechanism: grpcview.json is a COMMITTED, hand-editable file, so a `git pull`
// or a branch switch changes which sources a collection lists without going through any RPC.
func sourceListDigest(sources []*grpcviewv1.DescriptorSource) [sha256.Size]byte {
	bare := make([]*grpcviewv1.DescriptorSource, 0, len(sources))
	for _, src := range sources {
		clone := proto.CloneOf(src)
		clone.Resolved = nil
		bare = append(bare, clone)
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(&grpcviewv1.Collection{Sources: bare})
	if err != nil {
		return sha256.Sum256(nil)
	}
	return sha256.Sum256(data)
}

const definitionsCacheSize = 16

type definitionsCache struct {
	mu      sync.Mutex
	entries map[string]*definitions
	recent  []string
	// epochs counts how many times each collection has been invalidated. A derivation is not atomic with
	// the write that invalidates it, so without this a reader that started before a write can finish after
	// it and memoize the view it derived from the OLD blobs — and, the invalidation having already
	// happened, that stale entry survives until the next write. A reader carries the epoch it read at and
	// its store is refused if the epoch moved under it. Deleting the entry cannot express this, since
	// "absent" is indistinguishable from "never derived".
	epochs map[string]uint64
}

func newDefinitionsCache() *definitionsCache {
	return &definitionsCache{entries: map[string]*definitions{}, epochs: map[string]uint64{}}
}

func (c *definitionsCache) epoch(key string) uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epochs[key]
}

func (c *definitionsCache) lookup(key string) *definitions {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	defs, ok := c.entries[key]
	if !ok {
		return nil
	}
	c.touch(key)
	return defs
}

func (c *definitionsCache) store(key string, epoch uint64, defs *definitions) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.epochs[key] != epoch {
		return
	}
	c.entries[key] = defs
	c.touch(key)
	for len(c.recent) > definitionsCacheSize {
		delete(c.entries, c.recent[0])
		c.recent = c.recent[1:]
	}
}

func (c *definitionsCache) invalidate(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	c.recent = slices.DeleteFunc(c.recent, func(k string) bool { return k == key })
	c.epochs[key]++
}

func (c *definitionsCache) touch(key string) {
	c.recent = append(slices.DeleteFunc(c.recent, func(k string) bool { return k == key }), key)
}

var errUnresolvedSource = errors.New("not resolved yet — refresh this source")

func (w Workspace) definitions(ctx context.Context, collectionID string) (*definitions, error) {
	coll, err := w.store.Open(ctx, collectionID)
	if err != nil {
		return nil, toConnectError(err)
	}
	defs, err := w.definitionsOf(ctx, coll)
	if err != nil {
		return nil, err
	}
	if defs.mergeErr != nil {
		return nil, defs.mergeErr
	}
	if len(defs.descriptorSet) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"collection %q has no resolved definitions: add a descriptor source (or refresh one) first", collectionID))
	}
	return defs, nil
}

func (w Workspace) definitionsOf(ctx context.Context, coll *store.Collection) (*definitions, error) {
	if defs := w.defs.lookup(coll.Key()); defs != nil {
		return defs, nil
	}
	return w.derive(ctx, coll)
}

func (w Workspace) definitionsFor(ctx context.Context, coll *store.Collection, sources []*grpcviewv1.DescriptorSource) (*definitions, error) {
	digest := sourceListDigest(sources)
	if defs := w.defs.lookup(coll.Key()); defs != nil {
		if defs.listDigest == digest {
			return defs, nil
		}
		w.defs.invalidate(coll.Key())
	}
	return w.derive(ctx, coll)
}

func (w Workspace) derive(ctx context.Context, coll *store.Collection) (*definitions, error) {
	// The epoch is read BEFORE the blobs: a write landing during the derivation must be able to refuse the
	// result, which it can only do if the epoch we quote predates the read.
	epoch := w.defs.epoch(coll.Key())
	defs, err := w.deriveDefinitions(ctx, coll)
	if err != nil {
		return nil, err
	}
	w.defs.store(coll.Key(), epoch, defs)
	return defs, nil
}

// Merges what the store already holds. It NEVER acquires: a source with no blob is simply unresolved
// and contributes nothing, and no reflection target is dialed — dialing on a read would make opening a
// repo depend on every target in it being reachable.
func (w Workspace) deriveDefinitions(ctx context.Context, coll *store.Collection) (*definitions, error) {
	sources, err := coll.Sources(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	stored, err := coll.DescriptorResolves(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}

	resolved := make([]*resolvedSource, 0, len(sources))
	for _, src := range sources {
		rs := &resolvedSource{id: src.GetId(), server: src.GetReflection()}
		if blob, ok := stored[rs.id]; ok {
			rs.files, rs.services = blob.GetDescriptorSet(), blob.GetServiceNames()
		} else {
			rs.err = unresolvedReason(src)
		}
		resolved = append(resolved, rs)
	}

	view, err := mergeSources(resolved)
	if err != nil {
		summaries := summarize(resolved)
		for _, summary := range summaries {
			if summary.GetError() == "" {
				summary.Error = err.Error()
			}
		}
		return &definitions{
			sources:    withSummaries(sources, summaries),
			mergeErr:   err,
			listDigest: sourceListDigest(sources),
		}, nil
	}
	return &definitions{
		services:      view.serviceDescs,
		sources:       withSummaries(sources, view.summaries),
		serviceList:   view.services,
		descriptorSet: view.descriptorSet,
		listDigest:    sourceListDigest(sources),
	}, nil
}

func withSummaries(sources []*grpcviewv1.DescriptorSource, summaries map[string]*grpcviewv1.Resolved) []*grpcviewv1.DescriptorSource {
	for _, src := range sources {
		src.Resolved = summaries[src.GetId()]
	}
	return sources
}

func (w Workspace) loadCollection(ctx context.Context, coll *store.Collection) (*grpcviewv1.Collection, error) {
	ws, err := coll.Load(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	defs, err := w.definitionsFor(ctx, coll, ws.GetSources())
	if err != nil {
		return nil, err
	}
	ws.Services = defs.serviceList
	ws.DescriptorSet = defs.descriptorSet
	byID := make(map[string]*grpcviewv1.Resolved, len(defs.sources))
	for _, src := range defs.sources {
		byID[src.GetId()] = src.GetResolved()
	}
	for _, src := range ws.GetSources() {
		src.Resolved = byID[src.GetId()]
	}
	return ws, nil
}

func (d *definitions) method(collectionID, service, method string) (*desc.MethodDescriptor, error) {
	serviceDesc, ok := d.services[service]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf(
			"service %q is not in collection %q's definitions", service, collectionID))
	}
	methodDesc := serviceDesc.FindMethodByName(method)
	if methodDesc == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf(
			"method %q is not in service %q", method, service))
	}
	return methodDesc, nil
}

func (d *definitions) wonBy(service string) string {
	for _, src := range d.sources {
		for _, name := range src.GetResolved().GetWonServiceNames() {
			if name == service {
				return src.GetId()
			}
		}
	}
	return ""
}

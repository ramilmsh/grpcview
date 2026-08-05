package workspace

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"connectrpc.com/connect"

	"github.com/jhump/protoreflect/desc"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

// The descriptor sources own definition resolution end to end: they dial (on add and refresh
// only), and the store keeps what each one resolved to as a content-addressed blob. Everything
// downstream — Describe AND Invoke — reads the merged view derived here and never resolves again.
// Invoke therefore works against any target that speaks gRPC, whether or not it serves
// reflection; dialing is the only thing it still needs the network for.
//
// The merged view is DERIVED, never persisted: it is a pure function of (the blobs, the source
// order in grpcview.json), so writing it to disk would be a cache of a cache that every mutation
// had to rewrite — including a reorder, which changes no descriptor at all.

// definitions is one collection's merged view.
type definitions struct {
	// services is the merged descriptor set, linked and indexed by service full name.
	services map[string]*desc.ServiceDescriptor
	// sources is the manifest's list in priority order, each carrying its Resolved summary.
	sources []*grpcviewv1.DescriptorSource
	// serviceList and descriptorSet are the same merge in wire form, handed straight to a
	// client by loadCollection.
	serviceList   []*grpcviewv1.Service
	descriptorSet []byte
	// mergeErr is a merge that could not complete — sources disagreeing about a proto they
	// share. It is held rather than returned so that a load still succeeds; see
	// deriveDefinitions.
	mergeErr error
}

// definitionsCacheSize bounds the memo. One entry is a fully linked descriptor set, which for a
// monorepo schema is megabytes, so an unbounded map in a long-lived process serving a repo full
// of collections is a slow leak. The bound trades a re-derive of the coldest collection — N blob
// reads and one link, local CPU and no network — for that.
const definitionsCacheSize = 16

// definitionsCache memoizes the merged view per collection, keyed by the collection's store Key
// and by NOTHING else: a hit has to be a plain map lookup, with no read, no stat and no hash,
// because this memo is now the only copy of the merged view the process has and every invoke goes
// through it. An earlier version keyed on a digest of the merged descriptor-set bytes, which
// meant a hit still paid a full file read plus a full hash to prove itself valid. The writer
// invalidates instead (see putDescriptorState).
type definitionsCache struct {
	mu      sync.Mutex
	entries map[string]*definitions
	// recent is the eviction order, least-recently-used first.
	recent []string
	// epochs counts how many times each collection has been invalidated. A derivation is not
	// atomic with the write that invalidates it, so without this a reader that started before a
	// write can finish after it and memoize the view it derived from the OLD blobs — and, because
	// the invalidation it raced has already happened, that stale entry then survives until the
	// next write. The reader carries the epoch it read at and its store is refused if the epoch
	// moved under it; the next reader re-derives. Deleting an entry alone cannot express this,
	// since "absent" is indistinguishable from "never derived".
	epochs map[string]uint64
}

func newDefinitionsCache() *definitionsCache {
	return &definitionsCache{entries: map[string]*definitions{}, epochs: map[string]uint64{}}
}

// epoch is the invalidation count a reader must pass back to store what it derives. A nil cache
// never hits, so its epoch is irrelevant.
func (c *definitionsCache) epoch(key string) uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epochs[key]
}

// A nil cache is a working cache that just never hits, which keeps Workspace usable as a bare
// struct literal.
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

// store memoizes defs only if key has not been invalidated since the caller read epoch from it.
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

// touch moves key to the most-recently-used end; callers hold c.mu.
func (c *definitionsCache) touch(key string) {
	c.recent = append(slices.DeleteFunc(c.recent, func(k string) bool { return k == key }), key)
}

// errUnresolvedSource is what a source with no blob contributes: nothing, with a reason. It stays
// close to the collection-level wording a caller with no definitions at all gets, because from
// the user's side it is the same situation seen one row down.
var errUnresolvedSource = errors.New("not resolved yet — refresh this source")

// definitions resolves one collection's merged view for a CONSUMER of it (describe, invoke), for
// which a collection with nothing merged is a FailedPrecondition naming the fix. Callers that
// merely want to hand a Collection back to a client want loadCollection instead, which must not fail
// for either reason.
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

// definitionsOf hands back the collection's merged view, deriving it on first touch.
//
// Two concurrent first touches can both derive; that is deliberate rather than a lock held across
// the work, since the derivation is pure and idempotent, and serializing every collection's first
// read behind one mutex costs more than the duplicate link it would avoid.
func (w Workspace) definitionsOf(ctx context.Context, coll *store.Collection) (*definitions, error) {
	if defs := w.defs.lookup(coll.Key()); defs != nil {
		return defs, nil
	}
	// Read the epoch BEFORE the blobs: a write landing during the derivation below must be able
	// to refuse the result, which it can only do if the epoch we quote predates the read.
	epoch := w.defs.epoch(coll.Key())
	defs, err := w.deriveDefinitions(ctx, coll)
	if err != nil {
		return nil, err
	}
	w.defs.store(coll.Key(), epoch, defs)
	return defs, nil
}

// deriveDefinitions merges what the store already holds. It NEVER acquires: a source with no blob
// is simply unresolved and contributes nothing, and no reflection target is dialed here. Dialing
// on a read would make opening a repo depend on every target in it being reachable, and it is why
// resolveConfigured's dial-on-miss lives on the four acquisition RPCs and nowhere else.
func (w Workspace) deriveDefinitions(ctx context.Context, coll *store.Collection) (*definitions, error) {
	// The manifest owns which sources exist and in what priority order; the blobs only say what
	// each one last resolved to.
	sources, err := coll.Sources(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	blobs, err := coll.DescriptorBlobs(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}

	resolved := make([]*resolvedSource, 0, len(sources))
	for _, src := range sources {
		rs := &resolvedSource{id: src.GetId(), server: src.GetReflection()}
		if blob, ok := blobs[rs.id]; ok {
			rs.files, rs.services = blob.GetDescriptorSet(), blob.GetServiceNames()
		} else {
			rs.err = errUnresolvedSource
		}
		resolved = append(resolved, rs)
	}

	view, err := mergeSources(resolved)
	if err != nil {
		// A source list that cannot be merged must not stop the collection loading. The merge
		// used to run only while a user was mutating the sources; deriving it on first touch
		// means a grpcview.json a colleague committed could otherwise make Get fail and take the
		// tree, the scripts and the source rows down with it. Report it on every source — they
		// are jointly implicated, and the message names them — and yield no services, which is
		// the shape a fresh clone with no blobs already has.
		summaries := summarize(resolved)
		for _, summary := range summaries {
			if summary.GetError() == "" {
				summary.Error = err.Error()
			}
		}
		return &definitions{sources: withSummaries(sources, summaries), mergeErr: err}, nil
	}
	return &definitions{
		services:      view.serviceDescs,
		sources:       withSummaries(sources, view.summaries),
		serviceList:   view.services,
		descriptorSet: view.descriptorSet,
	}, nil
}

func withSummaries(sources []*grpcviewv1.DescriptorSource, summaries map[string]*grpcviewv1.Resolved) []*grpcviewv1.DescriptorSource {
	for _, src := range sources {
		src.Resolved = summaries[src.GetId()]
	}
	return sources
}

// loadCollection is the one way a handler turns a store.Collection into the wire Collection a
// client sees: the store's Load, plus the derived half it deliberately leaves empty (services,
// descriptor_set, and each source's Resolved summary). Every RPC returning a Collection goes
// through here, so what a client sees cannot depend on which handler answered — and neither an
// unresolved nor an unmergeable source list fails it, because the tree is answerable either way.
func (w Workspace) loadCollection(ctx context.Context, coll *store.Collection) (*grpcviewv1.Collection, error) {
	ws, err := coll.Load(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	defs, err := w.definitionsOf(ctx, coll)
	if err != nil {
		return nil, err
	}
	ws.Services = defs.serviceList
	ws.DescriptorSet = defs.descriptorSet
	// Overlaid by id, not assigned wholesale: the loaded list is this response's own, and the
	// summaries are shared with every other reader of the memo.
	byID := make(map[string]*grpcviewv1.Resolved, len(defs.sources))
	for _, src := range defs.sources {
		byID[src.GetId()] = src.GetResolved()
	}
	for _, src := range ws.GetSources() {
		src.Resolved = byID[src.GetId()]
	}
	return ws, nil
}

// method finds one method, naming the collection in both misses: at this point the caller has
// already been told the collection HAS definitions, so "not found" means "not in them".
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

// wonBy reports which source's descriptors the service was taken from.
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

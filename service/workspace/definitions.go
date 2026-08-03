package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"

	"connectrpc.com/connect"

	"github.com/jhump/protoreflect/desc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

// The descriptor sources own definition resolution end to end: they dial, they cache per source
// (store's state/sources/), and they re-derive the merged set on add/refresh/reorder/remove.
// Everything downstream — Describe AND Invoke — reads that merged set and never resolves again.
// Invoke therefore works against any target that speaks gRPC, whether or not it serves reflection;
// dialing is the only thing it still needs the network for.

// definitions is one merged descriptor set, linked and indexed by service full name.
type definitions struct {
	services map[string]*desc.ServiceDescriptor
	sources  []*grpcviewv1.DescriptorSource
}

// definitionsCache memoizes the linking step, which is the expensive half and pure: identical
// descriptor-set bytes always link to identical descriptors. Keyed by workspace and invalidated by
// the bytes' digest, so it holds one entry per workspace no matter how often sources are refreshed.
type definitionsCache struct {
	mu      sync.Mutex
	entries map[string]definitionsEntry
}

type definitionsEntry struct {
	digest [sha256.Size]byte
	defs   *definitions
}

func newDefinitionsCache() *definitionsCache {
	return &definitionsCache{entries: map[string]definitionsEntry{}}
}

// A nil cache is a working cache that just never hits, which keeps Workspace usable as a bare
// struct literal.
func (c *definitionsCache) lookup(workspaceName string, digest [sha256.Size]byte) *definitions {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[workspaceName]; ok && e.digest == digest {
		return e.defs
	}
	return nil
}

func (c *definitionsCache) store(workspaceName string, digest [sha256.Size]byte, defs *definitions) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[workspaceName] = definitionsEntry{digest: digest, defs: defs}
}

// definitions loads the workspace's merged descriptor set and links it. It never dials.
func (w Workspace) definitions(ctx context.Context, workspaceName string) (*definitions, error) {
	coll, err := w.store.Open(ctx, workspaceName)
	if err != nil {
		return nil, err
	}
	return w.definitionsOf(ctx, coll, workspaceName)
}

func (w Workspace) definitionsOf(ctx context.Context, coll *store.Collection, workspaceName string) (*definitions, error) {
	merged, err := coll.Merged(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	raw := merged.GetDescriptorSet()
	if len(raw) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"workspace %q has no resolved definitions: add a descriptor source (or refresh one) first", workspaceName))
	}

	digest := sha256.Sum256(raw)
	if cached := w.defs.lookup(workspaceName, digest); cached != nil {
		// The summaries can change without the descriptors changing (a reorder shuffles which
		// source WON a service), so those are re-read rather than cached with the linked set.
		return &definitions{services: cached.services, sources: merged.GetSources()}, nil
	}

	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, fds); err != nil {
		return nil, fmt.Errorf("parse the workspace's descriptor set: %w", err)
	}
	files, err := desc.CreateFileDescriptorsFromSet(fds)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("link the workspace's descriptor set: %w", err))
	}
	byName := make(map[string]*desc.ServiceDescriptor)
	for _, fd := range files {
		for _, sd := range fd.GetServices() {
			byName[sd.GetFullyQualifiedName()] = sd
		}
	}

	w.defs.store(workspaceName, digest, &definitions{services: byName})
	return &definitions{services: byName, sources: merged.GetSources()}, nil
}

// method finds one method, naming the workspace in both misses: at this point the caller has
// already been told the workspace HAS definitions, so "not found" means "not in them".
func (d *definitions) method(workspaceName, service, method string) (*desc.MethodDescriptor, error) {
	serviceDesc, ok := d.services[service]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf(
			"service %q is not in workspace %q's definitions", service, workspaceName))
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

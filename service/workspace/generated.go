package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting/codegen"
)

const generateTimeout = 2 * time.Minute

// generator serves protoc-gen-es output per file from a content-addressed cache (memory LRU over
// a disk CAS shared by every daemon, see codegen.Cache), keyed by codegen.Keys' Merkle key. It
// needs no invalidation: a changed file — or any file it transitively imports — is a different
// key. Misses go to the pool, which starts on first miss: the in-process CLI never pays for it.
type generator struct {
	poolMu sync.Mutex
	pool   *codegen.Pool
	closed bool

	cache *codegen.Cache

	// In-flight keys. Dedup is per key, not per job: collections sharing protos (the common
	// case — a shared definition, WKT-heavy imports) wait on each other's overlapping files
	// rather than generating them twice, even when their miss sets differ.
	mu       sync.Mutex
	inflight map[codegen.Key]*genEntry

	runs  atomic.Int64 // Generate jobs actually run; tests assert cache hits with it
	files atomic.Int64 // files those jobs generated
}

// done closes once content/err are final.
type genEntry struct {
	done    chan struct{}
	content string
	err     error
}

type claim struct {
	name string
	key  codegen.Key
	e    *genEntry
}

// newGenerator caches on disk under dir ("" = memory only).
func newGenerator(dir string) *generator {
	return &generator{cache: codegen.NewCache(dir), inflight: map[codegen.Key]*genEntry{}}
}

// generate returns proto file name -> generated TypeScript for every non-WKT file in set.
func (g *generator) generate(ctx context.Context, set []byte) (map[string]string, error) {
	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(set, &fds); err != nil {
		return nil, fmt.Errorf("unmarshal descriptor set: %w", err)
	}
	bundle, err := codegen.BundleHash()
	if err != nil {
		return nil, err
	}
	keys, err := codegen.Keys(bundle, fds.GetFile())
	if err != nil {
		return nil, err
	}

	waits := map[string]*genEntry{}
	var mine []claim
	g.mu.Lock()
	for _, f := range fds.GetFile() {
		name := f.GetName()
		if codegen.WKT(name) {
			continue
		}
		k := keys[name]
		e, ok := g.inflight[k]
		if !ok {
			e = &genEntry{done: make(chan struct{})}
			g.inflight[k] = e
			mine = append(mine, claim{name, k, e})
		}
		waits[name] = e
	}
	g.mu.Unlock()
	if len(mine) > 0 {
		// Detached: one caller giving up must not fail the others waiting on the same keys.
		go g.run(context.WithoutCancel(ctx), mine, fds.GetFile())
	}

	out := make(map[string]string, len(waits))
	for name, e := range waits {
		select {
		case <-e.done:
			if e.err != nil {
				return nil, e.err
			}
			out[name] = e.content
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return out, nil
}

// run resolves claims from the cache, then generates every miss in ONE job over the union of
// their closures. Errors aren't cached: a timeout or a closed pool says nothing about the file.
func (g *generator) run(ctx context.Context, claims []claim, files []*descriptorpb.FileDescriptorProto) {
	var misses []claim
	for _, c := range claims {
		if content, ok := g.cache.Get(c.key); ok {
			g.resolve(c, content, nil)
		} else {
			misses = append(misses, c)
		}
	}
	if len(misses) == 0 {
		return
	}
	names := make([]string, len(misses))
	for i, c := range misses {
		names[i] = c.name
	}
	generated, err := g.generateFiles(ctx, files, names)
	for _, c := range misses {
		content, ok := generated[codegen.OutputName(c.name)]
		switch {
		case err != nil:
			g.resolve(c, "", err)
		case !ok:
			g.resolve(c, "", fmt.Errorf("codegen produced no %s", codegen.OutputName(c.name)))
		default:
			g.cache.Put(c.key, content)
			g.resolve(c, content, nil)
		}
	}
}

func (g *generator) generateFiles(ctx context.Context, files []*descriptorpb.FileDescriptorProto, names []string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, generateTimeout)
	defer cancel()
	closure, err := codegen.Closure(files, names)
	if err != nil {
		return nil, err
	}
	set, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: closure})
	if err != nil {
		return nil, err
	}
	pool, err := g.poolFor(ctx)
	if err != nil {
		return nil, err
	}
	g.runs.Add(1)
	g.files.Add(int64(len(names)))
	return pool.Generate(ctx, set, names)
}

// resolve publishes c's result; a successful one is already cached, so a caller arriving after
// the inflight entry is gone hits the cache instead of regenerating.
func (g *generator) resolve(c claim, content string, err error) {
	c.e.content, c.e.err = content, err
	g.mu.Lock()
	delete(g.inflight, c.key)
	g.mu.Unlock()
	close(c.e.done)
}

func (g *generator) poolFor(ctx context.Context) (*codegen.Pool, error) {
	g.poolMu.Lock()
	defer g.poolMu.Unlock()
	if g.closed {
		return nil, codegen.ErrClosed
	}
	if g.pool == nil {
		pool, err := codegen.New(ctx, 1)
		if err != nil {
			return nil, err
		}
		g.pool = pool
	}
	return g.pool, nil
}

func (g *generator) Close(ctx context.Context) error {
	if g == nil {
		return nil
	}
	g.poolMu.Lock()
	defer g.poolMu.Unlock()
	g.closed = true
	if g.pool == nil {
		return nil
	}
	return g.pool.Close(ctx)
}

func (w Workspace) GetDefinitions(ctx context.Context, request *connect.Request[grpcviewv1.GetDefinitionsRequest]) (*connect.Response[grpcviewv1.GetDefinitionsResponse], error) {
	id := request.Msg.GetCollection()
	coll, err := w.store.Open(ctx, id)
	if err != nil {
		return nil, toConnectError(err)
	}
	sources, err := coll.Sources(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	// definitionsFor, like Get: a hand-edited grpcview.json must not be served stale.
	defs, err := w.definitionsFor(ctx, coll, sources)
	if err != nil {
		return nil, err
	}
	if defs.mergeErr != nil {
		return nil, defs.mergeErr
	}
	if len(defs.descriptorSet) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"collection %q has no resolved definitions: add a descriptor source (or refresh one) first", id))
	}
	if defs.digestErr != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("identify the type generator: %w", defs.digestErr))
	}
	if w.gen == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("type generation is not available in this process"))
	}

	generated, err := w.gen.generate(ctx, defs.descriptorSet)
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return nil, ctx.Err() // connect maps Canceled/DeadlineExceeded itself
	case errors.Is(err, codegen.ErrClosed):
		return nil, connect.NewError(connect.CodeUnavailable, err)
	default:
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf(
			"failed to generate TypeScript for collection %q's definitions: %w", id, err))
	}

	files := make([]*grpcviewv1.DefinitionFile, 0, len(generated))
	for name, content := range generated {
		files = append(files, &grpcviewv1.DefinitionFile{
			Path:    codegen.OutputName(name),
			Content: content,
			Source:  defs.claimedBy[name],
		})
	}
	slices.SortFunc(files, func(a, b *grpcviewv1.DefinitionFile) int { return strings.Compare(a.GetPath(), b.GetPath()) })
	for _, f := range protobufStubs {
		files = append(files, proto.Clone(f).(*grpcviewv1.DefinitionFile))
	}
	protos, err := defs.protoFiles()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf(
			"failed to render collection %q's definitions as .proto: %w", id, err))
	}
	return connect.NewResponse(&grpcviewv1.GetDefinitionsResponse{
		Digest: defs.digest,
		Files:  files,
		Protos: protos,
	}), nil
}

// stubsDigest covers protobufStubs, which GetDefinitions serves alongside the generated files.
var stubsDigest = func() [sha256.Size]byte {
	h := sha256.New()
	for _, f := range protobufStubs {
		writeLenPrefixed(h, []byte(f.GetPath()))
		writeLenPrefixed(h, []byte(f.GetContent()))
	}
	return [sha256.Size]byte(h.Sum(nil))
}()

// definitionsDigest identifies GetDefinitions' output: the generator, the merged set it runs
// over, each file's winning source, the winners' original .proto texts, and the builtin stubs.
// Rendered .proto is a function of the set (protoPrinter is fixed per binary).
func definitionsDigest(set []byte, claimedBy, originals map[string]string) (string, error) {
	bundle, err := codegen.BundleHash()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(bundle[:])
	h.Write(stubsDigest[:])
	writeLenPrefixed(h, set)
	for _, name := range slices.Sorted(maps.Keys(claimedBy)) {
		writeLenPrefixed(h, []byte(name))
		writeLenPrefixed(h, []byte(claimedBy[name]))
	}
	h.Write([]byte{0}) // separates the two lists
	for _, name := range slices.Sorted(maps.Keys(originals)) {
		writeLenPrefixed(h, []byte(name))
		writeLenPrefixed(h, []byte(originals[name]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeLenPrefixed(h hash.Hash, b []byte) {
	h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(b))))
	h.Write(b)
}

package codegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"codeberg.org/ramilmsh/grpcview/service/scripting"
)

// Key content-addresses one file's generated TypeScript.
type Key [sha256.Size]byte

func (k Key) String() string { return hex.EncodeToString(k[:]) }

// WKT reports whether name is a well-known type, imported from @bufbuild/protobuf/wkt and
// never generated.
func WKT(name string) bool { return strings.HasPrefix(name, "google/protobuf/") }

// OutputName is the file protoc-gen-es emits for proto file name.
func OutputName(name string) string { return strings.TrimSuffix(name, ".proto") + "_pb.ts" }

// Keys returns a Merkle key per file: sha256(bundle hash, plugin parameter, deterministic
// FileDescriptorProto, sorted keys of its direct deps). Sound because a file's output depends
// only on its own descriptor and its transitive imports (the per-file golden gate).
func Keys(bundleHash [sha256.Size]byte, files []*descriptorpb.FileDescriptorProto) (map[string]Key, error) {
	byName := index(files)
	keys := make(map[string]Key, len(files))
	visiting := map[string]bool{}
	var visit func(name string) (Key, error)
	visit = func(name string) (Key, error) {
		if k, ok := keys[name]; ok {
			return k, nil
		}
		f := byName[name]
		if f == nil {
			return Key{}, fmt.Errorf("codegen: missing dependency %q", name)
		}
		if visiting[name] {
			return Key{}, fmt.Errorf("codegen: import cycle through %q", name)
		}
		visiting[name] = true
		deps := make([]Key, 0, len(f.GetDependency()))
		for _, d := range f.GetDependency() {
			k, err := visit(d)
			if err != nil {
				return Key{}, err
			}
			deps = append(deps, k)
		}
		slices.SortFunc(deps, func(a, b Key) int { return bytes.Compare(a[:], b[:]) })
		raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(f)
		if err != nil {
			return Key{}, fmt.Errorf("codegen: marshal %q: %w", name, err)
		}
		h := sha256.New()
		h.Write(bundleHash[:])
		fmt.Fprintf(h, "%d:%s%d:", len(scripting.CodegenParameter), scripting.CodegenParameter, len(raw))
		h.Write(raw)
		for _, d := range deps {
			h.Write(d[:])
		}
		k := Key(h.Sum(nil))
		keys[name] = k
		return k, nil
	}
	for _, f := range files {
		if _, err := visit(f.GetName()); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// Closure returns the union of roots' transitive import closures, dependencies first.
func Closure(files []*descriptorpb.FileDescriptorProto, roots []string) ([]*descriptorpb.FileDescriptorProto, error) {
	byName := index(files)
	seen := map[string]bool{}
	var out []*descriptorpb.FileDescriptorProto
	var visit func(name string) error
	visit = func(name string) error {
		if seen[name] {
			return nil
		}
		seen[name] = true
		f := byName[name]
		if f == nil {
			return fmt.Errorf("codegen: missing dependency %q", name)
		}
		for _, d := range f.GetDependency() {
			if err := visit(d); err != nil {
				return err
			}
		}
		out = append(out, f)
		return nil
	}
	for _, r := range roots {
		if err := visit(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func index(files []*descriptorpb.FileDescriptorProto) map[string]*descriptorpb.FileDescriptorProto {
	m := make(map[string]*descriptorpb.FileDescriptorProto, len(files))
	for _, f := range files {
		m[f.GetName()] = f
	}
	return m
}

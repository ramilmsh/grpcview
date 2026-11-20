// Package protofiles compiles .proto sources in-process (protocompile), keeping comments and the
// original text of every file it read.
package protofiles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/linker"
	"github.com/bufbuild/protocompile/reporter"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Resolver returns a file's text by import name ("pkg/v1/x.proto"). An error wrapping fs.ErrNotExist
// means "not mine": the standard imports (google/protobuf/*) are tried next.
type Resolver interface {
	Read(importName string) ([]byte, error)
}

// Roots resolves an import name against each directory in order, like protoc's -I: first hit wins.
type Roots []string

func (r Roots) Read(importName string) ([]byte, error) {
	p, err := r.Locate(importName)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// Locate returns the on-disk path importName resolves to.
func (r Roots) Locate(importName string) (string, error) {
	if err := checkImportName(importName); err != nil {
		return "", err
	}
	for _, root := range r {
		p := filepath.Join(root, filepath.FromSlash(importName))
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: %w", importName, fs.ErrNotExist)
}

// ImportName is p relative to the first root containing it, slash-separated; false if none does.
func (r Roots) ImportName(p string) (string, bool) {
	for _, root := range r {
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		return filepath.ToSlash(rel), true
	}
	return "", false
}

// Paths resolves import names through an explicit map to files on disk.
type Paths map[string]string

func (m Paths) Read(importName string) ([]byte, error) {
	p, ok := m[importName]
	if !ok {
		return nil, fmt.Errorf("%s: %w", importName, fs.ErrNotExist)
	}
	return os.ReadFile(p)
}

// ResolverFunc adapts a function to Resolver.
type ResolverFunc func(importName string) ([]byte, error)

func (f ResolverFunc) Read(importName string) ([]byte, error) { return f(importName) }

func checkImportName(name string) error {
	if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
		return fmt.Errorf("%q is not a clean relative import name: %w", name, fs.ErrNotExist)
	}
	return nil
}

// Result of Compile.
type Result struct {
	// Compiled files and their transitive deps (standard imports included), deps first, deduped.
	// SourceCodeInfo is retained.
	Set *descriptorpb.FileDescriptorSet
	// Import name -> exact text, for every file the Resolver supplied (never builtin standard imports).
	Originals map[string]string
}

// Compile compiles files (import names) and everything they import.
func Compile(ctx context.Context, r Resolver, files ...string) (*Result, error) {
	var mu sync.Mutex
	originals := make(map[string]string)
	resolver := protocompile.WithStandardImports(protocompile.ResolverFunc(func(name string) (protocompile.SearchResult, error) {
		raw, err := r.Read(name)
		if err != nil {
			return protocompile.SearchResult{}, err
		}
		mu.Lock()
		originals[name] = string(raw)
		mu.Unlock()
		return protocompile.SearchResult{Source: bytes.NewReader(raw)}, nil
	}))

	var errs []error
	compiler := protocompile.Compiler{
		Resolver:       resolver,
		SourceInfoMode: protocompile.SourceInfoStandard,
		Reporter: reporter.NewReporter(func(err reporter.ErrorWithPos) error {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
			return nil
		}, nil),
	}
	linked, err := compiler.Compile(ctx, files...)
	if len(errs) > 0 {
		slices.SortStableFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
		return nil, errors.Join(errs...)
	}
	if err != nil {
		return nil, err
	}

	set := &descriptorpb.FileDescriptorSet{}
	seen := make(map[string]bool)
	var visit func(fd protoreflect.FileDescriptor)
	visit = func(fd protoreflect.FileDescriptor) {
		if seen[fd.Path()] {
			return
		}
		seen[fd.Path()] = true
		imports := fd.Imports()
		for i := range imports.Len() {
			visit(imports.Get(i).FileDescriptor)
		}
		set.File = append(set.File, fileProto(fd))
	}
	for _, fd := range linked {
		visit(fd)
	}
	return &Result{Set: set, Originals: originals}, nil
}

func fileProto(fd protoreflect.FileDescriptor) *descriptorpb.FileDescriptorProto {
	if res, ok := fd.(linker.Result); ok {
		return res.FileDescriptorProto()
	}
	return protodesc.ToFileDescriptorProto(fd)
}

// Find returns every *.proto under dir, recursively, sorted.
func Find(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".proto") {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

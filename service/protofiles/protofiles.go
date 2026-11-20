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
	"github.com/bufbuild/protocompile/reporter"
	"github.com/jhump/protoreflect/desc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Roots resolves an import name against each directory in order, like protoc's -I: first hit wins.
type Roots []string

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
	// Import name -> exact text, for every file read supplied, google/protobuf/* excluded (never rendered).
	Originals map[string]string
}

// Compile compiles files (import names) and everything they import. read returns a file's text by
// import name ("pkg/v1/x.proto"); an error wrapping fs.ErrNotExist means "not mine": the standard
// imports (google/protobuf/*) are tried next.
func Compile(ctx context.Context, read func(importName string) ([]byte, error), files ...string) (*Result, error) {
	var mu sync.Mutex
	originals := make(map[string]string)
	resolver := protocompile.WithStandardImports(protocompile.ResolverFunc(func(name string) (protocompile.SearchResult, error) {
		raw, err := read(name)
		if err != nil {
			return protocompile.SearchResult{}, err
		}
		if !strings.HasPrefix(name, "google/protobuf/") {
			mu.Lock()
			originals[name] = string(raw)
			mu.Unlock()
		}
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

	fds := make([]protoreflect.FileDescriptor, len(linked))
	for i, f := range linked {
		fds[i] = f
	}
	wrapped, err := desc.WrapFiles(fds)
	if err != nil {
		return nil, err
	}
	return &Result{Set: desc.ToFileDescriptorSet(wrapped...), Originals: originals}, nil
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

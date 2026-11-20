package scripting

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/evanw/esbuild/pkg/api"
)

func TestCodegenBundleHashTracksInputs(t *testing.T) {
	reg := fstest.MapFS{"npm/a/index.js": {Data: []byte("a")}, "npm/b.js": {Data: []byte("b")}}
	patches := map[string][][2]string{"a/index.js": {{"x", "y"}}}
	opts := codegenBuildOptions("")
	hash := func(reg fstest.MapFS, poly string, patches map[string][][2]string, entry, version string) [32]byte {
		t.Helper()
		o := opts
		stdin := *o.Stdin
		stdin.Contents = entry
		o.Stdin = &stdin
		h, err := codegenInputsHash(reg, "npm", poly, patches, o, version)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	base := hash(reg, "poly", patches, "entry", "1.0")
	if base != hash(reg, "poly", patches, "entry", "1.0") {
		t.Fatal("hash not deterministic")
	}
	for name, h := range map[string][32]byte{
		"registry content": hash(fstest.MapFS{"npm/a/index.js": {Data: []byte("A")}, "npm/b.js": {Data: []byte("b")}}, "poly", patches, "entry", "1.0"),
		"registry path":    hash(fstest.MapFS{"npm/a/index.js": {Data: []byte("a")}, "npm/c.js": {Data: []byte("b")}}, "poly", patches, "entry", "1.0"),
		"registry added":   hash(fstest.MapFS{"npm/a/index.js": {Data: []byte("a")}, "npm/b.js": {Data: []byte("b")}, "npm/c.js": {}}, "poly", patches, "entry", "1.0"),
		"polyfill":         hash(reg, "poly2", patches, "entry", "1.0"),
		"patch old":        hash(reg, "poly", map[string][][2]string{"a/index.js": {{"x2", "y"}}}, "entry", "1.0"),
		"patch new":        hash(reg, "poly", map[string][][2]string{"a/index.js": {{"x", "y2"}}}, "entry", "1.0"),
		"patch target":     hash(reg, "poly", map[string][][2]string{"b.js": {{"x", "y"}}}, "entry", "1.0"),
		"entry":            hash(reg, "poly", patches, "entry2", "1.0"),
		"esbuild":          hash(reg, "poly", patches, "entry", "1.1"),
	} {
		if h == base {
			t.Errorf("%s changed, hash didn't", name)
		}
	}
	o := opts
	o.Target++
	if h, _ := codegenInputsHash(reg, "npm", "poly", patches, o, "1.0"); h == base {
		t.Error("options changed, hash didn't")
	}
	if v, err := esbuildVersion(goMod); err != nil || !strings.HasPrefix(v, "v0.") {
		t.Errorf("esbuildVersion = %q, %v", v, err)
	}
	if v, _ := esbuildVersion("require (\n\tgithub.com/evanw/esbuild v1.2.3 // indirect\n)\n"); v != "v1.2.3" {
		t.Errorf("esbuildVersion(block) = %q", v)
	}
	if v, _ := esbuildVersion("require github.com/evanw/esbuild v1.2.4\n"); v != "v1.2.4" {
		t.Errorf("esbuildVersion(line) = %q", v)
	}
	if _, err := CodegenBundleHash(); err != nil {
		t.Fatal(err)
	}
}

func TestMemoPluginFilterSeparatorAgnostic(t *testing.T) {
	var filter string
	plugin, _ := memoPlugin(memoPatches)
	plugin.Setup(api.PluginBuild{OnLoad: func(o api.OnLoadOptions, _ func(api.OnLoadArgs) (api.OnLoadResult, error)) {
		filter = o.Filter
	}})
	re := regexp.MustCompile(filter)
	for _, p := range []string{
		"/tmp/r/@bufbuild/protoplugin/dist/cjs/names.js",
		`C:\r\@bufbuild\protoplugin\dist\cjs\source-code-info.js`,
	} {
		if !re.MatchString(p) {
			t.Errorf("filter %q misses %s", filter, p)
		}
	}
	if re.MatchString("/tmp/r/@bufbuild/protoplugin/dist/cjs/xnames.js") {
		t.Error("filter matches a mere suffix of a file name")
	}
}

func TestBuildCodegenBundleFailsOnUnappliedPatch(t *testing.T) {
	dir, err := MaterializeNpmRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if _, err := BuildCodegenBundle(dir); err != nil {
		t.Fatalf("BuildCodegenBundle: %v", err)
	}
	patches := map[string][][2]string{"@bufbuild/protoplugin/dist/cjs/renamed.js": {{"a", "b"}}}
	for k, v := range memoPatches {
		patches[k] = v
	}
	if _, err := buildCodegenBundle(dir, patches); err == nil || !strings.Contains(err.Error(), "renamed.js") {
		t.Fatalf("err = %v, want renamed.js not applied", err)
	}
}

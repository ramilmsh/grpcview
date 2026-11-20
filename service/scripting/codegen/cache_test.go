package codegen

import (
	"crypto/sha256"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func key(s string) Key { return Key(sha256.Sum256([]byte(s))) }

func TestCacheDisk(t *testing.T) {
	dir := t.TempDir()
	a, b := key("a"), key("b")
	NewCache(dir).Put(a, "export const a = 1;\n")
	NewCache(dir).Put(b, "export const b = 2;\n")

	fresh := NewCache(dir) // a new process: nothing in memory
	if got, ok := fresh.Get(a); !ok || got != "export const a = 1;\n" {
		t.Fatalf("Get(a) = %q, %v; want disk hit", got, ok)
	}
	if _, ok := NewCache(dir).Get(key("c")); ok {
		t.Fatal("Get(c): want miss")
	}

	p := fresh.path(b)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for name, corrupt := range map[string]string{
		"flipped": strings.Replace(string(raw), "= 2", "= 3", 1),
		"short":   string(raw[:len(raw)-3]),
		"header":  "garbage",
		"empty":   "",
	} {
		if err := os.WriteFile(p, []byte(corrupt), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, ok := NewCache(dir).Get(b); ok {
			t.Errorf("%s: Get = %q, want miss", name, got)
		}
	}
}

func TestCacheMemoryBound(t *testing.T) {
	c := NewCache("")
	c.memMax = 10
	c.Put(key("a"), "aaaaaa")
	c.Put(key("b"), "bbbbbb")
	if _, ok := c.Get(key("a")); ok {
		t.Error("a: want evicted")
	}
	if _, ok := c.Get(key("b")); !ok {
		t.Error("b: want kept")
	}
}

func TestCacheSweepEvictsOldest(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)
	c.diskMax = 400
	content := strings.Repeat("x", 100) // + ~90 bytes of header per entry
	old := time.Now().Add(-time.Hour)
	for i, s := range []string{"a", "b", "c"} {
		c.Put(key(s), content)
		c.sweeps.Wait()
		_ = os.Chtimes(c.path(key(s)), old.Add(time.Duration(i)*time.Minute), old.Add(time.Duration(i)*time.Minute))
	}
	c.sinceSweep = -1 // force the next Put to sweep
	c.Put(key("d"), content)
	c.sweeps.Wait()
	for s, want := range map[string]bool{"a": false, "b": false, "c": true, "d": true} {
		if _, err := os.Stat(c.path(key(s))); (err == nil) != want {
			t.Errorf("%s on disk = %v, want %v", s, err == nil, want)
		}
	}
}

// The sweep runs off the Put path, one at a time.
func TestCacheSweepInBackground(t *testing.T) {
	c := NewCache(t.TempDir())
	c.diskMax = 4 // every Put is past the threshold
	release := make(chan struct{})
	var runs atomic.Int32
	c.sweepFn = func() {
		runs.Add(1)
		<-release
	}
	c.Put(key("a"), "aaaa") // returns while the sweep blocks
	c.Put(key("b"), "bbbb") // one already running: no second
	close(release)
	c.sweeps.Wait()
	if n := runs.Load(); n != 1 {
		t.Fatalf("sweeps = %d, want 1", n)
	}
	c.Put(key("c"), "cccc")
	c.sweeps.Wait()
	if n := runs.Load(); n != 2 {
		t.Fatalf("sweeps after the first finished = %d, want 2", n)
	}
}

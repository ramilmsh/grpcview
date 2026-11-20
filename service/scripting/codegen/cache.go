package codegen

import (
	"bufio"
	"bytes"
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	memMax  = 64 << 20  // in-memory LRU, bytes of content
	diskMax = 256 << 20 // disk CAS: a sweep trims back to diskMax*3/4, oldest mtime first
	tmpAge  = time.Hour // orphaned temp files (a writer died mid-write) older than this go too
	header  = "// grpcview-codegen sha256:"
)

// Cache maps Key -> generated TypeScript: a byte-bounded in-memory LRU in front of a disk CAS
// at <dir>/<aa>/<key>.ts. Entries are immutable and content-addressed, so concurrent daemons
// share dir safely: writes are temp+rename, a read verifies the header's checksum (anything
// short, corrupt or unreadable is a miss), and a disk hit bumps mtime so the sweep is
// approximately LRU across processes; it runs in the background, at most one at a time, on a
// process's first write and every diskMax/4 written after. Disk failures only cost
// regeneration, never an error.
type Cache struct {
	dir string // "" = memory only

	mu    sync.Mutex
	lru   *list.List // of *memEntry, front = most recent
	items map[Key]*list.Element
	bytes int

	sweepMu    sync.Mutex
	sinceSweep int // bytes written since the last sweep; -1 = none yet this process
	sweeping   bool
	sweeps     sync.WaitGroup // tests Wait on it
	sweepFn    func()         // c.sweep; tests swap it
	memMax     int
	diskMax    int64
}

type memEntry struct {
	key     Key
	content string
}

func NewCache(dir string) *Cache {
	c := &Cache{dir: dir, lru: list.New(), items: map[Key]*list.Element{}, sinceSweep: -1, memMax: memMax, diskMax: diskMax}
	c.sweepFn = c.sweep
	return c
}

func (c *Cache) path(k Key) string {
	s := k.String()
	return filepath.Join(c.dir, s[:2], s+".ts")
}

func (c *Cache) Get(k Key) (string, bool) {
	c.mu.Lock()
	if e, ok := c.items[k]; ok {
		c.lru.MoveToFront(e)
		c.mu.Unlock()
		return e.Value.(*memEntry).content, true
	}
	c.mu.Unlock()
	if c.dir == "" {
		return "", false
	}
	p := c.path(k)
	content, ok := readEntry(p)
	if !ok {
		return "", false
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now)
	c.remember(k, content)
	return content, true
}

func (c *Cache) Put(k Key, content string) {
	c.remember(k, content)
	if c.dir == "" {
		return
	}
	if err := writeEntry(c.path(k), content); err != nil {
		return
	}
	c.sweepMu.Lock()
	defer c.sweepMu.Unlock()
	if c.sinceSweep >= 0 && c.sinceSweep < int(c.diskMax/4) {
		c.sinceSweep += len(content)
		return
	}
	if c.sweeping {
		return // the next Put retries
	}
	c.sinceSweep, c.sweeping = 0, true
	c.sweeps.Add(1)
	go func() {
		defer c.sweeps.Done()
		c.sweepFn()
		c.sweepMu.Lock()
		c.sweeping = false
		c.sweepMu.Unlock()
	}()
}

func (c *Cache) remember(k Key, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[k]; ok {
		c.lru.MoveToFront(e)
		return
	}
	c.items[k] = c.lru.PushFront(&memEntry{key: k, content: content})
	c.bytes += len(content)
	for c.bytes > c.memMax && c.lru.Len() > 1 {
		e := c.lru.Back()
		m := c.lru.Remove(e).(*memEntry)
		delete(c.items, m.key)
		c.bytes -= len(m.content)
	}
}

func readEntry(p string) (string, bool) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	line, content, ok := bytes.Cut(raw, []byte("\n"))
	sum, ok2 := strings.CutPrefix(string(line), header)
	if !ok || !ok2 {
		return "", false
	}
	want, err := hex.DecodeString(sum)
	got := sha256.Sum256(content)
	if err != nil || !bytes.Equal(want, got[:]) {
		return "", false
	}
	return string(content), true
}

func writeEntry(p, content string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(content))
	w := bufio.NewWriter(f)
	_, _ = w.WriteString(header + hex.EncodeToString(sum[:]) + "\n")
	_, _ = w.WriteString(content)
	err = w.Flush()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), p)
	}
	if err != nil {
		_ = os.Remove(f.Name())
	}
	return err
}

// sweep deletes the least recently used entries until the CAS is back under 3/4 of diskMax.
// Racing sweeps from other daemons are harmless: deletes are idempotent, and a reader losing
// its entry mid-flight just misses.
func (c *Cache) sweep() {
	type entry struct {
		path  string
		size  int64
		mtime time.Time
	}
	var entries []entry
	var total int64
	_ = filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".tmp-") {
			if time.Since(info.ModTime()) > tmpAge {
				_ = os.Remove(p)
			}
			return nil
		}
		entries = append(entries, entry{p, info.Size(), info.ModTime()})
		total += info.Size()
		return nil
	})
	if total <= c.diskMax {
		return
	}
	slices.SortFunc(entries, func(a, b entry) int { return a.mtime.Compare(b.mtime) })
	for _, e := range entries {
		if total <= c.diskMax*3/4 {
			break
		}
		if os.Remove(e.path) == nil {
			total -= e.size
		}
	}
}

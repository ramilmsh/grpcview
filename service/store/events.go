package store

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
)

// Event names the workspace paths one mutation touched.
type Event struct {
	// Paths are workspace-rooted POSIX ("/" is the workspace root, ResolvePath's input form), sorted.
	// Each is a file, or the root of a created, moved or removed directory. Shared: do not modify.
	Paths []string
	// Resync: the subscriber fell behind and events were dropped; re-read everything. Paths is nil.
	Resync bool
}

// Queued events per subscriber; one more overflows the queue into a single Resync.
const subscriberQueue = 64

type hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// Subscribe delivers one Event per mutation, in commit order, until ctx is done; then the channel
// closes. Publishing never blocks: a subscriber that falls subscriberQueue events behind gets one
// Resync in place of them, then resumes.
func (s *Store) Subscribe(ctx context.Context) <-chan Event {
	ch := make(chan Event, subscriberQueue)
	s.hub.mu.Lock()
	s.hub.subs[ch] = struct{}{}
	s.hub.mu.Unlock()
	context.AfterFunc(ctx, func() {
		s.hub.mu.Lock()
		defer s.hub.mu.Unlock()
		delete(s.hub.subs, ch)
		close(ch)
	})
	return ch
}

func (s *Store) publish(paths []string) {
	if len(paths) == 0 {
		return
	}
	slices.Sort(paths)
	ev := Event{Paths: slices.Compact(paths)}
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	for ch := range s.hub.subs {
		select {
		case ch <- ev:
			continue
		default:
		}
		// Full: ev is dropped too, since the re-read a Resync triggers happens after it committed.
		// Only publish sends, under hub.mu, so after the drain the Resync fits.
		for len(ch) > 0 {
			select {
			case <-ch:
			default:
			}
		}
		ch <- Event{Resync: true}
	}
}

// workspacePath maps abs into the workspace namespace; false when it lies outside (the state root).
// s.root is canonical, the form wsroot.Confine output is in.
func (s *Store) workspacePath(abs string) (string, bool) {
	rel, err := filepath.Rel(s.root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return path.Join("/", filepath.ToSlash(rel)), true
}

// touched records the workspace paths one mutation writes, for exactly one Event. Every workspace
// write goes through its methods. State-root writes (history, descriptor index, blobs) bypass it:
// they are not in the workspace.
type touched struct {
	s     *Store
	paths []string
}

func (t *touched) add(abs string) {
	if p, ok := t.s.workspacePath(abs); ok {
		t.paths = append(t.paths, p)
	}
}

// record adds ps when err is nil, and returns err.
func (t *touched) record(err error, ps ...string) error {
	if err == nil {
		for _, p := range ps {
			t.add(p)
		}
	}
	return err
}

func (t *touched) publish() { t.s.publish(t.paths) }

func (t *touched) writeMessage(p string, m proto.Message) error {
	return t.record(writeMessage(p, m), p)
}

func (t *touched) writeSource(p, src string) error { return t.record(writeSourceFile(p, src), p) }

func (t *touched) writeFile(p string, data []byte) error {
	return t.record(writeFileAtomic(p, data, 0o644), p)
}

// mkdirAll records each directory it creates.
func (t *touched) mkdirAll(dir string) error {
	var missing []string
	for d := dir; filepath.Dir(d) != d; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil {
			break
		}
		missing = append(missing, d)
	}
	err := os.MkdirAll(dir, 0o755)
	for _, d := range missing {
		if _, statErr := os.Lstat(d); statErr == nil {
			t.add(d)
		}
	}
	return err
}

func (t *touched) remove(p string) error { return t.record(os.Remove(p), p) }

// removeAll records p even on failure: part of the tree may already be gone.
func (t *touched) removeAll(p string) error {
	err := os.RemoveAll(p)
	t.add(p)
	return err
}

func (t *touched) rename(from, to string) error { return t.record(os.Rename(from, to), from, to) }

// lockWrite takes c.mu for a mutation; the returned unlock publishes what it touched, then releases.
// Publishing under c.mu keeps one collection's events in commit order.
func (c *Collection) lockWrite() func() {
	c.mu.Lock()
	c.tx = &touched{s: c.store}
	return func() {
		tx := c.tx
		c.tx = nil
		tx.publish()
		c.mu.Unlock()
	}
}

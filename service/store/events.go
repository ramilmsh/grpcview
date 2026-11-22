package store

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sync"

	"google.golang.org/protobuf/proto"

	"codeberg.org/ramilmsh/grpcview/service/wsroot"
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
	subs map[*subscriber]struct{}
}

type subscriber struct {
	mu    sync.Mutex
	queue []Event
	wake  chan struct{}
}

// Subscribe delivers one Event per mutation, in commit order, until ctx is done; then the channel
// closes. Publishing never blocks: a subscriber that falls subscriberQueue events behind gets one
// Resync in place of them, then resumes.
func (s *Store) Subscribe(ctx context.Context) <-chan Event {
	sub := &subscriber{wake: make(chan struct{}, 1)}
	s.hub.mu.Lock()
	s.hub.subs[sub] = struct{}{}
	s.hub.mu.Unlock()

	out := make(chan Event)
	go func() {
		defer close(out)
		defer func() {
			s.hub.mu.Lock()
			delete(s.hub.subs, sub)
			s.hub.mu.Unlock()
		}()
		for {
			ev, ok := sub.pop()
			if !ok {
				select {
				case <-sub.wake:
					continue
				case <-ctx.Done():
					return
				}
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func (sub *subscriber) push(ev Event) {
	sub.mu.Lock()
	if len(sub.queue) >= subscriberQueue {
		// ev is dropped too: the re-read a Resync triggers happens after it committed.
		sub.queue = []Event{{Resync: true}}
	} else {
		sub.queue = append(sub.queue, ev)
	}
	sub.mu.Unlock()
	select {
	case sub.wake <- struct{}{}:
	default:
	}
}

func (sub *subscriber) pop() (Event, bool) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if len(sub.queue) == 0 {
		return Event{}, false
	}
	ev := sub.queue[0]
	sub.queue = sub.queue[1:]
	return ev, true
}

func (s *Store) publish(paths []string) {
	if len(paths) == 0 {
		return
	}
	slices.Sort(paths)
	ev := Event{Paths: slices.Compact(paths)}
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	for sub := range s.hub.subs {
		sub.push(ev)
	}
}

// workspacePath maps abs into the workspace namespace; false when it lies outside (the state root).
// Lexical against the root first, then its symlink-resolved form, which wsroot.Confine output is in.
func (s *Store) workspacePath(abs string) (string, bool) {
	for _, root := range []string{s.root, s.realRoot()} {
		if !wsroot.Within(root, abs) {
			continue
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		return path.Join("/", filepath.ToSlash(rel)), true
	}
	return "", false
}

func (s *Store) realRoot() string {
	s.realRootOnce.Do(func() {
		s.realRootPath = s.root
		if abs, err := filepath.Abs(s.root); err == nil {
			if real, err := filepath.EvalSymlinks(abs); err == nil {
				s.realRootPath = real
			}
		}
	})
	return s.realRootPath
}

// touched records the workspace paths one mutation writes, for exactly one Event. Every workspace
// write goes through its methods; a nil *touched writes without recording. State-root writes
// (history, descriptor index, blobs) bypass it: they are not in the workspace.
type touched struct {
	s     *Store
	paths []string
}

func (t *touched) add(abs string) {
	if t == nil {
		return
	}
	if p, ok := t.s.workspacePath(abs); ok {
		t.paths = append(t.paths, p)
	}
}

func (t *touched) publish() { t.s.publish(t.paths) }

func (t *touched) writeMessage(p string, m proto.Message) error {
	err := writeMessage(p, m)
	if err == nil {
		t.add(p)
	}
	return err
}

func (t *touched) writeSource(p, src string) error {
	err := writeSourceFile(p, src)
	if err == nil {
		t.add(p)
	}
	return err
}

func (t *touched) writeFile(p string, data []byte) error {
	err := writeFileAtomic(p, data, 0o644)
	if err == nil {
		t.add(p)
	}
	return err
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

func (t *touched) remove(p string) error {
	err := os.Remove(p)
	if err == nil {
		t.add(p)
	}
	return err
}

// removeAll records p even on failure: part of the tree may already be gone.
func (t *touched) removeAll(p string) error {
	err := os.RemoveAll(p)
	t.add(p)
	return err
}

func (t *touched) rename(from, to string) error {
	err := os.Rename(from, to)
	if err == nil {
		t.add(from)
		t.add(to)
	}
	return err
}

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

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
)

const sentinel = "/sentinel"

// drain returns every event published before a sentinel it publishes itself.
func drain(t *testing.T, s *Store, ch <-chan Event) []Event {
	t.Helper()
	s.publish([]string{sentinel})
	var got []Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("subscription closed early")
			}
			if slices.Equal(ev.Paths, []string{sentinel}) {
				return got
			}
			got = append(got, ev)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for events")
		}
	}
}

func subscribe(t *testing.T, s *Store) <-chan Event {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return s.Subscribe(ctx)
}

func committedState(id string) DescriptorState {
	src := reflectionSourceAt("localhost:50051")
	src.CommitDescriptors = true
	return DescriptorState{
		Sources:  []*grpcviewv1.DescriptorSource{src},
		Resolves: map[string]*grpcviewstorev1.ResolvedSource{id: {Id: id, DescriptorSet: fdsNamed("a.proto")}},
	}
}

func TestMutationsPublishOneEventNamingEveryTouchedPath(t *testing.T) {
	const srcID = "reflection:localhost:50051"
	str := func(s string) *string { return &s }
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	withRequest := func(t *testing.T, c *Collection, ctx context.Context) {
		must(t, c.CreateFolder(ctx, nil, "users"))
		must(t, c.CreateRequest(ctx, []string{"users"}, "get", "S", "M"))
	}
	sidecar := "/test/descriptors/" + hashedName(srcID) + descriptorSidecarExt

	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *Collection, context.Context)
		op    func(*Collection, context.Context) error
		want  []string
	}{
		{
			name: "Create",
			op: func(c *Collection, ctx context.Context) error {
				fresh, err := c.store.Open(ctx, "fresh")
				if err != nil {
					return err
				}
				return fresh.Create(ctx, "")
			},
			want: []string{"/fresh", "/fresh/grpcview.json", "/fresh/tree"},
		},
		{
			name: "SetName",
			op:   func(c *Collection, ctx context.Context) error { return c.SetName(ctx, "n") },
			want: []string{"/test/grpcview.json"},
		},
		{
			name: "CreateFolder",
			op:   func(c *Collection, ctx context.Context) error { return c.CreateFolder(ctx, nil, "users") },
			want: []string{"/test/grpcview.json", "/test/tree/users", "/test/tree/users/folder.json", "/test/tree/users/metadata.ts"},
		},
		{
			name:  "CreateRequest",
			setup: func(t *testing.T, c *Collection, ctx context.Context) { must(t, c.CreateFolder(ctx, nil, "users")) },
			op: func(c *Collection, ctx context.Context) error {
				return c.CreateRequest(ctx, []string{"users"}, "get", "S", "M")
			},
			want: []string{
				"/test/tree/users/folder.json", "/test/tree/users/get", "/test/tree/users/get/body.ts",
				"/test/tree/users/get/metadata.ts", "/test/tree/users/get/request.json",
			},
		},
		{
			name:  "UpdateRequest",
			setup: withRequest,
			op: func(c *Collection, ctx context.Context) error {
				return c.UpdateRequest(ctx, []string{"users"}, "get", RequestPatch{Name: str("got"), DraftBody: str("{}"), DraftMetadataScript: str("x")})
			},
			want: []string{"/test/tree/users/get/body.ts", "/test/tree/users/get/metadata.ts", "/test/tree/users/get/request.json"},
		},
		{
			name:  "UpdateFolder",
			setup: withRequest,
			op: func(c *Collection, ctx context.Context) error {
				return c.UpdateFolder(ctx, nil, "users", FolderPatch{Name: str("people"), DraftMetadataScript: str("x")})
			},
			want: []string{"/test/tree/users/folder.json", "/test/tree/users/metadata.ts"},
		},
		{
			name:  "Delete",
			setup: withRequest,
			op:    func(c *Collection, ctx context.Context) error { return c.Delete(ctx, []string{"users"}, "get") },
			want:  []string{"/test/tree/users/folder.json", "/test/tree/users/get"},
		},
		{
			name:  "Move across folders",
			setup: withRequest,
			op:    func(c *Collection, ctx context.Context) error { return c.Move(ctx, []string{"users"}, "get", nil, nil) },
			want:  []string{"/test/grpcview.json", "/test/tree/get", "/test/tree/users/folder.json", "/test/tree/users/get"},
		},
		{
			name: "Move within a folder",
			setup: func(t *testing.T, c *Collection, ctx context.Context) {
				must(t, c.CreateFolder(ctx, nil, "a"))
				must(t, c.CreateFolder(ctx, nil, "b"))
			},
			op:   func(c *Collection, ctx context.Context) error { return c.Move(ctx, nil, "b", nil, str("a")) },
			want: []string{"/test/grpcview.json"},
		},
		{
			name: "PutDescriptorState commits a sidecar",
			op: func(c *Collection, ctx context.Context) error {
				return c.PutDescriptorState(ctx, committedState(srcID))
			},
			want: []string{"/test/descriptors", sidecar, "/test/grpcview.json"},
		},
		{
			name: "PutDescriptorState prunes a sidecar",
			setup: func(t *testing.T, c *Collection, ctx context.Context) {
				must(t, c.PutDescriptorState(ctx, committedState(srcID)))
			},
			op:   func(c *Collection, ctx context.Context) error { return c.PutDescriptorState(ctx, DescriptorState{}) },
			want: []string{"/test/descriptors", sidecar, "/test/grpcview.json"},
		},
		{
			name: "CreateScript",
			op:   func(c *Collection, ctx context.Context) error { return c.CreateScript(ctx, "scripts/a/b.ts") },
			want: []string{"/test/scripts", "/test/scripts/a", "/test/scripts/a/b.ts"},
		},
		{
			name:  "UpdateScript with NewPath",
			setup: func(t *testing.T, c *Collection, ctx context.Context) { must(t, c.CreateScript(ctx, "scripts/a/b.ts")) },
			op: func(c *Collection, ctx context.Context) error {
				return c.UpdateScript(ctx, "scripts/a/b.ts", ScriptPatch{Source: str("x"), NewPath: str("scripts/c/d.ts")})
			},
			want: []string{"/test/scripts/a/b.ts", "/test/scripts/c", "/test/scripts/c/d.ts"},
		},
		{
			name: "DeleteScript prunes dirs",
			setup: func(t *testing.T, c *Collection, ctx context.Context) {
				must(t, c.CreateScript(ctx, "scripts/a/b/c.ts"))
			},
			op:   func(c *Collection, ctx context.Context) error { return c.DeleteScript(ctx, "scripts/a/b/c.ts") },
			want: []string{"/test/scripts/a", "/test/scripts/a/b", "/test/scripts/a/b/c.ts"},
		},
		{
			name: "Store.Rename",
			op: func(c *Collection, ctx context.Context) error {
				_, err := c.store.Rename(ctx, "test", "moved/test")
				return err
			},
			want: []string{"/moved", "/moved/test", "/test"},
		},
		{
			name: "partial failure",
			setup: func(t *testing.T, c *Collection, ctx context.Context) {
				must(t, c.CreateScript(ctx, "scripts/a.ts"))
				must(t, c.CreateScript(ctx, "scripts/b.ts"))
			},
			op: func(c *Collection, ctx context.Context) error {
				err := c.UpdateScript(ctx, "scripts/a.ts", ScriptPatch{Source: str("x"), NewPath: str("scripts/b.ts")})
				if !errors.Is(err, ErrAlreadyExists) {
					return fmt.Errorf("err = %v, want ErrAlreadyExists", err)
				}
				return nil
			},
			want: []string{"/test/scripts/a.ts"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ctx := newTestCollection(t)
			if tc.setup != nil {
				tc.setup(t, c, ctx)
			}
			ch := subscribe(t, c.store)
			if err := tc.op(c, ctx); err != nil {
				t.Fatal(err)
			}
			got := drain(t, c.store, ch)
			if len(got) != 1 {
				t.Fatalf("events = %v, want exactly one", got)
			}
			if got[0].Resync || !slices.Equal(got[0].Paths, tc.want) {
				t.Errorf("paths = %v, want %v", got[0].Paths, tc.want)
			}
		})
	}
}

func TestAppendHistoryPublishesNothing(t *testing.T) {
	c, ctx := newTestCollection(t)
	if err := c.CreateRequest(ctx, nil, "get", "S", "M"); err != nil {
		t.Fatal(err)
	}
	ch := subscribe(t, c.store)
	if err := c.AppendHistory(ctx, nil, "get", &grpcviewv1.History{}, 10); err != nil {
		t.Fatal(err)
	}
	if got := drain(t, c.store, ch); len(got) != 0 {
		t.Errorf("AppendHistory published %v", got)
	}
}

func TestOverflowingSubscriberGetsResyncThenResumes(t *testing.T) {
	s := newTestStore(t)
	ch := subscribe(t, s)
	const n = subscriberQueue + 10
	for i := range n {
		s.publish([]string{fmt.Sprintf("/%d", i)})
	}
	last := fmt.Sprintf("/%d", n-1)
	resync := false
	for {
		select {
		case ev := <-ch:
			if ev.Resync {
				resync = true
				continue
			}
			if ev.Paths[0] != last {
				continue
			}
			if !resync {
				t.Fatal("no Resync before the last event")
			}
			if got := drain(t, s, ch); len(got) != 0 {
				t.Errorf("after resuming: %v, want nothing", got)
			}
			return
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out; resync=%v", resync)
		}
	}
}

func TestCancelClosesSubscription(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	ch := s.Subscribe(ctx)
	s.publish([]string{"/x"})
	cancel()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				s.hub.mu.Lock()
				defer s.hub.mu.Unlock()
				if len(s.hub.subs) != 0 {
					t.Errorf("subscriber not removed: %d left", len(s.hub.subs))
				}
				return
			}
		case <-deadline:
			t.Fatal("channel not closed after cancel")
		}
	}
}

package wire

import (
	"context"
	"testing"
	"time"
)

func TestHeartbeatInterval(t *testing.T) {
	cases := []struct {
		idle time.Duration
		want time.Duration
	}{
		{time.Hour, maxHeartbeat},      // the shipped default: clamped, not idle/3
		{3 * time.Minute, time.Minute}, // a third of the window, three chances to be heard
		{time.Second, minHeartbeat},    // a beat per second is a busy loop, not resilience
		{30 * time.Minute, 10 * time.Minute},
	}
	for _, c := range cases {
		if got := heartbeatInterval(c.idle); got != c.want {
			t.Errorf("heartbeatInterval(%s) = %s, want %s", c.idle, got, c.want)
		}
	}
}

// Nothing to hold open, so nothing to hold it with: a server with no idle timeout — a hand-run
// one, or the in-process binding — ends the loop rather than beating at it forever.
func TestKeepalive_stopsWhenTheServerNeverIdlesOut(t *testing.T) {
	backend := &stub{idle: 0, beats: make(chan struct{}, 4)}
	client := Remote(serve(t, backend))

	done := make(chan struct{})
	go func() { Keepalive(context.Background(), client); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Keepalive kept beating at a server that never idles out")
	}
	if len(backend.beats) != 1 {
		t.Errorf("sent %d beats, want 1 — the one that asked", len(backend.beats))
	}
}

func TestKeepalive_beatsAndStopsWithItsContext(t *testing.T) {
	backend := &stub{idle: time.Hour, beats: make(chan struct{}, 4)}
	client := Remote(serve(t, backend))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Keepalive(ctx, client); close(done) }()

	select {
	case <-backend.beats:
	case <-time.After(5 * time.Second):
		t.Fatal("Keepalive never reached the server")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Keepalive outlived its context")
	}
}

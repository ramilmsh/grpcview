package service

import (
	"net/http"
	"sync"
	"time"
)

// A counter, not a timestamp: the deadline is only armed when nothing is in flight, so a
// server-streaming invoke that runs past it survives to completion rather than being killed
// mid-stream. The timestamp moves on both edges of a request, so a long call also counts as
// activity when it finishes.
type idleTimer struct {
	after time.Duration

	mu       sync.Mutex
	inflight int
	last     time.Time
}

func newIdleTimer(after time.Duration) *idleTimer {
	return &idleTimer{after: after, last: time.Now()}
}

func (t *idleTimer) wrap(next http.Handler) http.Handler {
	if t == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.enter()
		defer t.leave()
		next.ServeHTTP(w, r)
	})
}

func (t *idleTimer) enter() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inflight++
	t.last = time.Now()
}

func (t *idleTimer) leave() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inflight--
	t.last = time.Now()
}

func (t *idleTimer) expired(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.inflight == 0 && now.Sub(t.last) >= t.after
}

// remaining is how long the deadline is still away, never zero: a caller that woke early has to
// be given something to sleep on.
func (t *idleTimer) remaining(now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if left := t.after - now.Sub(t.last); left > 0 {
		return left
	}
	return t.after
}

// watch closes nothing and returns when done fires; it calls fire at most once. It sleeps to
// the deadline rather than polling towards it, so an idle daemon wakes once per window.
func (t *idleTimer) watch(done <-chan struct{}, fire func()) {
	if t == nil || t.after <= 0 {
		return
	}
	timer := time.NewTimer(t.after)
	defer timer.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-timer.C:
			if t.expired(now) {
				fire()
				return
			}
			timer.Reset(t.remaining(now))
		}
	}
}

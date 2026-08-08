package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIdleTimer_armedOnlyWhenNothingIsInFlight(t *testing.T) {
	timer := newIdleTimer(10 * time.Millisecond)
	timer.last = time.Now().Add(-time.Hour)

	if !timer.expired(time.Now()) {
		t.Fatal("an idle server did not expire")
	}

	// A server-streaming invoke that outruns the deadline must survive to completion, which a
	// last-request-time timestamp alone would not give it.
	timer.enter()
	timer.last = time.Now().Add(-time.Hour)
	if timer.expired(time.Now()) {
		t.Fatal("expired with a request in flight")
	}
	timer.leave()
	if timer.expired(time.Now()) {
		t.Fatal("expired immediately after a request finished")
	}
}

// The deadline is measured from the last request, not from startup: a server busy all day
// must never idle out, however long ago it started.
func TestIdleTimer_measuresTimeSinceTheLastRequest(t *testing.T) {
	timer := newIdleTimer(time.Hour)
	started := time.Now()
	timer.last = started.Add(-24 * time.Hour)

	timer.enter()
	timer.leave()

	if timer.expired(time.Now()) {
		t.Fatal("expired right after a request, on a timer created a day ago")
	}
}

func TestIdleTimer_wrapCountsRequests(t *testing.T) {
	timer := newIdleTimer(time.Hour)
	handler := timer.wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		timer.mu.Lock()
		defer timer.mu.Unlock()
		if timer.inflight != 1 {
			t.Errorf("inflight = %d during a request, want 1", timer.inflight)
		}
	}))

	srv := httptest.NewServer(handler)
	defer srv.Close()
	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	timer.mu.Lock()
	defer timer.mu.Unlock()
	if timer.inflight != 0 {
		t.Errorf("inflight = %d after the request, want 0", timer.inflight)
	}
}

func TestIdleTimer_watchFires(t *testing.T) {
	timer := newIdleTimer(100 * time.Millisecond)
	fired := make(chan struct{})
	done := make(chan struct{})
	defer close(done)
	go timer.watch(done, func() { close(fired) })

	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("the idle timer never fired")
	}
}

func TestIdleTimer_nilNeverFires(t *testing.T) {
	var timer *idleTimer
	handler := timer.wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if handler == nil {
		t.Fatal("wrap on a nil timer returned nil")
	}
	done := make(chan struct{})
	close(done)
	timer.watch(done, func() { t.Fatal("a nil idle timer fired") })
}

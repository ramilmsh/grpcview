package wire

import (
	"context"
	"time"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// A client that is connected but quiet is, to the server, indistinguishable from no client at
// all: the idle timer is armed by silence, not by disconnection. So a session that outlives the
// idle window has to say something to keep what it is holding.
const (
	beatTimeout  = 10 * time.Second
	minHeartbeat = 30 * time.Second
	maxHeartbeat = 10 * time.Minute
	beatsPerIdle = 3
)

// Keepalive holds a server open for as long as ctx lives, and returns when it dies or when the
// server turns out not to idle out at all — one that does not is nothing to hold open.
//
// The heartbeat is ServerInfo, so it lands on the same idle timer every real call does. Run it
// on the reconnecting binding and a failed beat also repairs the connection, so a session that
// has been quiet for an hour is already pointed at a live server when its next call arrives.
func Keepalive(ctx context.Context, c Client) {
	every := minHeartbeat
	for {
		if idle, ok := beat(ctx, c); ok {
			if idle <= 0 {
				return
			}
			every = heartbeatInterval(idle)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// A failed beat is not fatal: the server may be mid-restart, and the next beat is the retry.
// The interval survives the failure, so a server that comes back keeps the cadence it asked for.
func beat(ctx context.Context, c Client) (time.Duration, bool) {
	callCtx, cancel := context.WithTimeout(ctx, beatTimeout)
	defer cancel()
	res, err := c.ServerInfo(callCtx, connect.NewRequest(&grpcviewv1.ServerInfoRequest{}))
	if err != nil {
		return 0, false
	}
	return res.Msg.GetIdleTimeout().AsDuration(), true
}

// Read from the server rather than assumed, so a daemon started with a different --idle-timeout
// — or restarted by a rebuild under a new one — retunes the client that is holding it open.
func heartbeatInterval(idle time.Duration) time.Duration {
	every := idle / beatsPerIdle
	if every < minHeartbeat {
		return minHeartbeat
	}
	if every > maxHeartbeat {
		return maxHeartbeat
	}
	return every
}

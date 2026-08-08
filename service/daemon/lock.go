package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// A rendezvous lock, not a command lock: it covers check → spawn → wait → connect and is
// released the moment a client is connected. Held for the command's duration instead, it
// would serialize every concurrent invocation.
func lock(ctx context.Context, root string) (func(), error) {
	if _, err := ensureDir(); err != nil {
		return nil, err
	}
	path, err := lockPath(root)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open the spawn lock %q: %w", path, err)
	}

	// Polled non-blocking rather than a blocking LOCK_EX, which no context can interrupt.
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			file.Close()
			return nil, fmt.Errorf("failed to take the spawn lock %q: %w", path, err)
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

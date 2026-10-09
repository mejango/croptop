// Package ctxlock provides cancellable admission to existing synchronous gates.
package ctxlock

import (
	"context"
	"sync"
	"time"
)

// Lock acquires mu unless ctx is cancelled. On success the caller owns the lock
// and must unlock it. No background waiter can acquire the gate after return.
func Lock(ctx context.Context, mu *sync.Mutex) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if mu.TryLock() {
			if err := ctx.Err(); err != nil {
				mu.Unlock()
				return err
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

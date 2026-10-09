package ctxlock

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestLockOwnsGateUntilCallerUnlocks(t *testing.T) {
	var mu sync.Mutex
	if err := Lock(context.Background(), &mu); err != nil {
		t.Fatal(err)
	}
	if mu.TryLock() {
		t.Fatal("successful admission did not retain the gate")
	}
	mu.Unlock()
	if !mu.TryLock() {
		t.Fatal("gate not released by caller")
	}
	mu.Unlock()
}

func TestCancelledContextNeverAcquiresFreeGate(t *testing.T) {
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Lock(ctx, &mu); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled admission: %v", err)
	}
	if !mu.TryLock() {
		t.Fatal("cancelled admission retained the gate")
	}
	mu.Unlock()
}

func TestDeadlineDoesNotWaitForOwnerOrLeaveBackgroundWaiter(t *testing.T) {
	var mu sync.Mutex
	mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Lock(ctx, &mu) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline admission: %v", err)
		}
	case <-time.After(time.Second):
		mu.Unlock()
		t.Fatal("admission did not honor deadline while owner held gate")
	}
	mu.Unlock()
	if !mu.TryLock() {
		t.Fatal("cancelled waiter acquired the gate after returning")
	}
	mu.Unlock()
}

func TestWaitingAdmissionSucceedsAfterOwnerReleases(t *testing.T) {
	var mu sync.Mutex
	mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Lock(ctx, &mu) }()
	mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Unlock()
}

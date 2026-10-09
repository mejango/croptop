package render

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/store"
)

func TestCancelledRenderDoesNotCreatePublicTree(t *testing.T) {
	r, s := fixtureRenderer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Render(ctx, fixtureID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled render: %v", err)
	}
	if _, err := os.Stat(s.PublicDir(fixtureID)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("cancelled render touched public tree: %v", err)
	}
}

func TestRenderDeadlineWhileAnotherRenderOwnsGate(t *testing.T) {
	r, s := fixtureRenderer(t)
	r.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Render(ctx, fixtureID) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("render while gate held: %v", err)
		}
	case <-time.After(time.Second):
		r.mu.Unlock()
		t.Fatal("render did not return on deadline")
	}
	r.mu.Unlock()
	if _, err := os.Stat(s.PublicDir(fixtureID)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("cancelled waiter rendered after returning: %v", err)
	}
	if err := r.Render(context.Background(), fixtureID); err != nil {
		t.Fatalf("fresh render could not acquire released gate: %v", err)
	}
}

func TestCancellationDuringTemplateSelectionStopsBeforePosts(t *testing.T) {
	r, s := fixtureRenderer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.TemplateFor = func(*store.Site) (fs.FS, error) {
		cancel()
		return r.Templates, nil
	}
	if err := r.Render(ctx, fixtureID); !errors.Is(err, context.Canceled) {
		t.Fatalf("render after cancellation: %v", err)
	}
	posts, err := s.Posts(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	for _, post := range posts {
		if _, err := os.Stat(s.PublicDir(fixtureID) + "/" + post.ID); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("render proceeded to a post after cancellation: %v", err)
		}
	}
}

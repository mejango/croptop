package follow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mejango/croptop/internal/ipfs"
)

// fakeEngine answers like a network that holds one site.
type fakeEngine struct {
	ipfs.Engine // nil; only the methods below are used
	cid         string
	title       string
	gets        int
	provided    []string
}

func (f *fakeEngine) Resolve(ctx context.Context, p string) (string, error) {
	return "/ipns/k51fake", nil
}
func (f *fakeEngine) NetworkRecord(ctx context.Context, name string) (*ipfs.Record, error) {
	return &ipfs.Record{Value: "/ipfs/" + f.cid, Sequence: 1}, nil
}
func (f *fakeEngine) Get(ctx context.Context, ipfsPath, dest string) error {
	f.gets++
	os.MkdirAll(dest, 0o755)
	return os.WriteFile(filepath.Join(dest, "planet.json"), []byte(`{"id":"X","name":"`+f.title+`","articles":[]}`), 0o644)
}
func (f *fakeEngine) Provide(ctx context.Context, cid string) error {
	f.provided = append(f.provided, cid)
	return nil
}

func TestFollowRefreshUnfollow(t *testing.T) {
	eng := &fakeEngine{cid: "bafyONE", title: "One"}
	s := &Store{Root: t.TempDir(), Engine: eng}
	ctx := context.Background()
	e, err := s.Follow(ctx, "one.eth")
	if err != nil {
		t.Fatal(err)
	}
	if e.IPNS != "k51fake" || e.CID != "bafyONE" || e.Title != "One" || eng.gets != 1 {
		t.Fatalf("entry %+v gets %d", e, eng.gets)
	}
	if _, err := os.Stat(filepath.Join(s.SiteDir("k51fake"), "planet.json")); err != nil {
		t.Fatal("site not stored")
	}
	if len(eng.provided) != 1 || eng.provided[0] != "bafyONE" {
		t.Fatalf("provided %v", eng.provided)
	}
	// unchanged: no refetch, but re-provide
	if changed, err := s.Refresh(ctx, "k51fake"); err != nil || changed || eng.gets != 1 {
		t.Fatalf("unchanged refresh: %v %v gets %d", changed, err, eng.gets)
	}
	// changed: refetch
	eng.cid, eng.title = "bafyTWO", "Two"
	if changed, err := s.Refresh(ctx, "k51fake"); err != nil || !changed || eng.gets != 2 {
		t.Fatalf("changed refresh: %v %v gets %d", changed, err, eng.gets)
	}
	e, _ = s.Get("k51fake")
	if e.CID != "bafyTWO" || e.Title != "Two" || e.Changed == 0 {
		t.Fatalf("entry after change %+v", e)
	}
	list, _ := s.List()
	if len(list) != 1 {
		t.Fatalf("list %d", len(list))
	}
	if _, err := s.Follow(ctx, "k51fake"); err != nil {
		t.Fatal("following twice should be fine:", err)
	}
	if err := s.Unfollow("k51fake"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatal("still listed after unfollow")
	}
	if err := s.Unfollow("k51fake"); err == nil {
		t.Fatal("unfollowing twice should error")
	}
}

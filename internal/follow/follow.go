// Package follow keeps copies of other people's sites: fetched, served
// locally, and re-provided to the network so a site stays online because
// its readers host it.
package follow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/store"
)

type Entry struct {
	Name    string          `json:"name"`  // what the user typed: an ENS name or IPNS name
	IPNS    string          `json:"ipns"`  // resolved IPNS name, the folder key
	CID     string          `json:"cid"`   // last fetched root
	Title   string          `json:"title"` // planet.json name
	Checked store.AppleTime `json:"checked"`
	Changed store.AppleTime `json:"changed"`
	Error   string          `json:"error,omitempty"` // last refresh problem, cleared on success
}

type Store struct {
	Root   string
	Engine ipfs.Engine
	Log    func(string)

	mu sync.Mutex
}

func (s *Store) log(format string, a ...any) {
	if s.Log != nil {
		s.Log(fmt.Sprintf(format, a...))
	}
}

func (s *Store) dir(ipns string) string     { return filepath.Join(s.Root, "following", ipns) }
func (s *Store) SiteDir(ipns string) string { return filepath.Join(s.dir(ipns), "site") }
func (s *Store) entryPath(ipns string) string {
	return filepath.Join(s.dir(ipns), "follow.json")
}

func (s *Store) Get(ipns string) (*Entry, error) {
	b, err := os.ReadFile(s.entryPath(ipns))
	if err != nil {
		return nil, err
	}
	var e Entry
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *Store) save(e *Entry) error {
	if err := os.MkdirAll(s.dir(e.IPNS), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(e, "", "  ")
	return os.WriteFile(s.entryPath(e.IPNS), append(b, '\n'), 0o644)
}

func (s *Store) List() ([]*Entry, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, "following"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Entry
	for _, d := range entries {
		if e, err := s.Get(d.Name()); err == nil {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	return out, nil
}

// Follow resolves name to an IPNS name, fetches the site, and provides it.
func (s *Store) Follow(ctx context.Context, name string) (*Entry, error) {
	name = strings.TrimSpace(strings.TrimPrefix(name, "/ipns/"))
	if name == "" {
		return nil, errors.New("name is required")
	}
	ipns := name
	if !strings.HasPrefix(name, "k51") {
		resolved, err := s.Engine.Resolve(ctx, "/ipns/"+name)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", name, err)
		}
		if !strings.HasPrefix(resolved, "/ipns/") {
			return nil, fmt.Errorf("%s points at a fixed CID, not an IPNS name", name)
		}
		ipns = strings.TrimPrefix(resolved, "/ipns/")
	}
	if e, err := s.Get(ipns); err == nil {
		return e, nil // already following
	}
	e := &Entry{Name: name, IPNS: ipns}
	if err := s.save(e); err != nil {
		return nil, err
	}
	if _, err := s.Refresh(ctx, ipns); err != nil {
		os.RemoveAll(s.dir(ipns))
		return nil, err
	}
	return s.Get(ipns)
}

// Refresh fetches the current version when the IPNS record moved.
func (s *Store) Refresh(ctx context.Context, ipns string) (changed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.Get(ipns)
	if err != nil {
		return false, err
	}
	defer func() {
		e.Checked = store.Now()
		if err != nil {
			e.Error = err.Error()
		} else {
			e.Error = ""
		}
		s.save(e)
	}()
	rec, err := s.Engine.NetworkRecord(ctx, ipns)
	if err != nil {
		return false, fmt.Errorf("resolve: %w", err)
	}
	cid := strings.TrimPrefix(rec.Value, "/ipfs/")
	if cid == e.CID {
		if _, statErr := os.Stat(filepath.Join(s.SiteDir(ipns), "planet.json")); statErr == nil {
			s.Engine.Provide(ctx, cid)
			return false, nil
		}
	}
	tmp := filepath.Join(s.dir(ipns), "site.new")
	os.RemoveAll(tmp)
	if err := publish.FetchSite(ctx, s.Engine, ipns, cid, tmp, s.log); err != nil {
		return false, err
	}
	title := e.Title
	if b, err := os.ReadFile(filepath.Join(tmp, "planet.json")); err == nil {
		var pj struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(b, &pj) == nil && pj.Name != "" {
			title = pj.Name
		}
	}
	os.RemoveAll(s.SiteDir(ipns))
	if err := os.Rename(tmp, s.SiteDir(ipns)); err != nil {
		return false, err
	}
	e.CID, e.Title, e.Changed = cid, title, store.Now()
	if err := s.Engine.Provide(ctx, cid); err != nil {
		s.log("provide %s: %v", title, err)
	}
	s.log("following %s at %s", title, cid)
	return true, nil
}

func (s *Store) Unfollow(ipns string) error {
	if _, err := s.Get(ipns); err != nil {
		return fmt.Errorf("not following %s", ipns)
	}
	return os.RemoveAll(s.dir(ipns))
}

// ProvideAll re-announces every followed root.
func (s *Store) ProvideAll(ctx context.Context) {
	list, _ := s.List()
	for _, e := range list {
		if e.CID != "" {
			s.Engine.Provide(ctx, e.CID)
		}
	}
}

// Run refreshes every followed site now and then every interval.
func (s *Store) Run(ctx context.Context, every time.Duration) {
	refresh := func() {
		list, _ := s.List()
		for _, e := range list {
			if _, err := s.Refresh(ctx, e.IPNS); err != nil {
				s.log("refresh %s: %v", e.Title, err)
			}
		}
	}
	refresh()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			refresh()
		}
	}
}

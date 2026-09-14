// Package collaboration merges published source sites into a normal local site.
package collaboration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

var nameRE = regexp.MustCompile(`^k51[a-z0-9]{20,100}$`)
var postRE = regexp.MustCompile(`^[A-Fa-f0-9]{8}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{12}$`)

func ValidName(s string) bool { return nameRE.MatchString(s) }
func Sources(s *store.Site) []store.Contributor {
	cs := s.ContributorSites()
	for i := range cs {
		cs[i].Mode = "all"
	}
	return cs
}
func key(source, id string) string {
	h := sha256.Sum256([]byte(source + "/" + strings.ToUpper(id)))
	return hex.EncodeToString(h[:16])
}
func uuid(k string) string {
	return strings.ToUpper(k[:8] + "-" + k[8:12] + "-" + k[12:16] + "-" + k[16:20] + "-" + k[20:])
}
func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

type State struct {
	Contributors []store.Contributor `json:"contributors"`
	Errors       map[string]string   `json:"errors"`
	Updated      store.AppleTime     `json:"updated"`
}
type Record struct{ Source, SourcePost, LocalID, Digest string }
type diskState struct {
	Records []Record
	CIDs    map[string]string
	Names   map[string]string
	Errors  map[string]string
	Updated store.AppleTime
}
type Manager struct {
	Store *store.Store
	Node  ipfs.Engine
	mu    sync.Mutex
}

func (m *Manager) dir(id string) string { return filepath.Join(m.Store.SiteDir(id), "collaboration") }
func (m *Manager) read(id string) (diskState, error) {
	d := diskState{CIDs: map[string]string{}, Names: map[string]string{}, Errors: map[string]string{}}
	b, e := os.ReadFile(filepath.Join(m.dir(id), "merge.json"))
	if os.IsNotExist(e) {
		return d, nil
	}
	if e != nil {
		return d, e
	}
	e = json.Unmarshal(b, &d)
	return d, e
}
func (m *Manager) save(id string, d diskState) error {
	if e := os.MkdirAll(m.dir(id), 0700); e != nil {
		return e
	}
	b, e := json.Marshal(d)
	if e != nil {
		return e
	}
	p := filepath.Join(m.dir(id), "merge.json")
	if e = os.WriteFile(p+".tmp", b, 0600); e != nil {
		return e
	}
	return os.Rename(p+".tmp", p)
}
func state(s *store.Site, d diskState) State {
	cs := Sources(s)
	for i := range cs {
		if n := d.Names[cs[i].IPNS]; n != "" {
			cs[i].Name = n
		}
	}
	return State{Contributors: cs, Errors: d.Errors, Updated: d.Updated}
}
func (m *Manager) State(id string) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, e := m.Store.Site(id)
	if e != nil {
		return State{}, e
	}
	d, e := m.read(id)
	return state(s, d), e
}
func (m *Manager) AddSources(id string, cs []store.Contributor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, e := m.Store.Site(id)
	if e != nil {
		return e
	}
	all := Sources(s)
	for _, c := range cs {
		if !ValidName(c.IPNS) || c.IPNS == s.IPNS {
			return errors.New("choose another site's ENS name or IPNS address")
		}
		if len(c.Name) > 200 {
			return errors.New("source name is too long")
		}
		found := false
		for _, v := range all {
			if v.IPNS == c.IPNS {
				found = true
			}
		}
		if !found {
			c.Mode = "all"
			all = append(all, c)
		}
	}
	if len(all) > 50 {
		return errors.New("a curated site can combine up to 50 source sites")
	}
	s.Contributors = all
	s.Aggregation = []string{}
	s.Updated = store.Now()
	return m.Store.SaveSite(s)
}

// Sync mirrors source additions, edits and deletions. Failed sources retain their
// previous copies. No review decisions, invitation markers or publishing keys participate.
func (m *Manager) Sync(ctx context.Context, id string) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, e := m.Store.Site(id)
	if e != nil {
		return State{}, e
	}
	d, e := m.read(id)
	if e != nil {
		return State{}, e
	}
	d.Errors = map[string]string{}
	changed := false
	for _, c := range Sources(s) {
		if e = ctx.Err(); e != nil {
			return state(s, d), e
		}
		did, err := m.mergeSource(ctx, s, c, &d)
		if err != nil {
			d.Errors[c.IPNS] = err.Error()
		} else if did {
			changed = true
		}
	}
	if changed {
		s.Updated = store.Now()
		if e = m.Store.SaveSite(s); e != nil {
			return state(s, d), e
		}
	}
	d.Updated = store.Now()
	return state(s, d), m.save(id, d)
}
func (m *Manager) mergeSource(ctx context.Context, s *store.Site, c store.Contributor, d *diskState) (bool, error) {
	if !ValidName(c.IPNS) {
		return false, errors.New("this source needs an IPNS address")
	}
	if m.Node == nil {
		return false, errors.New("source connection unavailable")
	}
	rec, e := m.Node.NetworkRecord(ctx, c.IPNS)
	if e != nil {
		return false, fmt.Errorf("could not reach this source: %w", e)
	}
	if rec == nil || !strings.HasPrefix(rec.Value, "/ipfs/") {
		return false, errors.New("source has not published a site")
	}
	if d.CIDs[c.IPNS] == rec.Value {
		return false, nil
	}
	if e = os.MkdirAll(m.dir(s.ID), 0700); e != nil {
		return false, e
	}
	tmp, e := os.MkdirTemp(m.dir(s.ID), "merge-")
	if e != nil {
		return false, e
	}
	defer os.RemoveAll(tmp)
	tree := filepath.Join(tmp, "site")
	if e = m.Node.Get(ctx, rec.Value, tree); e != nil {
		return false, e
	}
	b, e := readFile(tree, "planet.json", 16<<20)
	if e != nil {
		return false, e
	}
	var pub struct {
		Name     string              `json:"name"`
		Articles []render.PublicPost `json:"articles"`
	}
	if e = json.Unmarshal(b, &pub); e != nil {
		return false, e
	}
	if len(pub.Articles) > 10000 {
		return false, errors.New("source has too many posts")
	}
	if pub.Name != "" {
		c.Name = pub.Name
	}
	existing, e := m.Store.Posts(s.ID)
	if e != nil {
		return false, e
	}
	type planned struct {
		post    *store.Post
		record  Record
		files   map[string]string
		changed bool
	}
	plans := []planned{}
	records := []Record{}
	seen := map[string]bool{}
	for _, a := range pub.Articles {
		if a.ArticleType != 0 {
			continue
		}
		if !postRE.MatchString(a.ID) {
			return false, errors.New("source contains an invalid post ID")
		}
		if seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		raw, err := readFile(tree, a.ID+"/article.json", 4<<20)
		if err != nil {
			return false, err
		}
		var full render.PublicPost
		if err = json.Unmarshal(raw, &full); err != nil {
			return false, err
		}
		if full.ID != a.ID {
			return false, errors.New("source post ID mismatch")
		}
		origin, originalID, author, date := c.IPNS, full.ID, c.Name, &full.Created
		if ValidName(full.OriginalSiteDomain) && postRE.MatchString(full.OriginalPostID) {
			origin, originalID, author, date = full.OriginalSiteDomain, full.OriginalPostID, full.OriginalSiteName, full.OriginalPostDate
		}
		// A source may itself include one of this site's own posts. Keep the original.
		if origin == s.IPNS {
			continue
		}
		localID := uuid(key(origin, originalID))
		for _, p := range existing {
			if p.OriginalSiteDomain == origin && strings.EqualFold(p.OriginalPostID, originalID) {
				localID = p.ID
				break
			}
		}
		p := &store.Post{ID: localID, Title: full.Title, Content: full.Content, Created: full.Created, Link: "/" + localID + "/", Attachments: full.Attachments, Tags: full.Tags, VideoFilename: full.VideoFilename, AudioFilename: full.AudioFilename, HeroImage: full.HeroImageFilename, OriginalSiteName: author, OriginalSiteDomain: origin, OriginalPostID: originalID, OriginalPostDate: date}
		if p.Attachments == nil {
			p.Attachments = []string{}
		}
		if len(p.Attachments) > 100 {
			return false, errors.New("source post has too many files")
		}
		files := map[string]string{}
		hash := sha256.New()
		hash.Write(raw)
		total := 0
		for _, name := range p.Attachments {
			if !safeFile(name) {
				return false, errors.New("source has an unsafe filename")
			}
			data, err := readFile(tree, a.ID+"/"+name, 50<<20)
			if err != nil {
				return false, err
			}
			total += len(data)
			if total > 200<<20 {
				return false, errors.New("source post exceeds 200 MB")
			}
			hash.Write([]byte(name))
			hash.Write(data)
			staged := filepath.Join(tmp, "posts", localID, name)
			if err = os.MkdirAll(filepath.Dir(staged), 0700); err != nil {
				return false, err
			}
			if err = os.WriteFile(staged, data, 0600); err != nil {
				return false, err
			}
			files[name] = staged
		}
		for _, field := range []**string{&p.HeroImage, &p.AudioFilename, &p.VideoFilename} {
			if *field != nil && !contains(p.Attachments, **field) {
				*field = nil
			}
		}
		record := Record{Source: c.IPNS, SourcePost: a.ID, LocalID: localID, Digest: hex.EncodeToString(hash.Sum(nil))}
		different := true
		for _, old := range d.Records {
			if old.Source == c.IPNS && old.SourcePost == a.ID && old.Digest == record.Digest {
				if _, err := m.Store.Post(s.ID, localID); err == nil {
					different = false
				}
			}
		}
		plans = append(plans, planned{p, record, files, different})
		records = append(records, record)
	}
	// Validate the whole source before mutating local posts or deleting stale copies.
	changed := false
	for _, plan := range plans {
		if !plan.changed {
			continue
		}
		changed = true
		dest := m.Store.PostDir(s.ID, plan.post.ID)
		if e = os.MkdirAll(dest, 0755); e != nil {
			return false, e
		}
		for name, staged := range plan.files {
			data, err := os.ReadFile(staged)
			if err != nil {
				return false, err
			}
			if e = os.WriteFile(filepath.Join(dest, name), data, 0644); e != nil {
				return false, e
			}
		}
		now := store.Now()
		plan.post.Modified = &now
		if e = m.Store.SavePost(s.ID, plan.post); e != nil {
			return false, e
		}
	}
	keep := []Record{}
	for _, old := range d.Records {
		if old.Source != c.IPNS {
			keep = append(keep, old)
		}
	}
	keep = append(keep, records...)
	for _, old := range d.Records {
		if old.Source != c.IPNS {
			continue
		}
		present := false
		for _, now := range keep {
			if now.LocalID == old.LocalID {
				present = true
				break
			}
		}
		if !present {
			if e = m.Store.DeletePost(s.ID, old.LocalID); e != nil && !errors.Is(e, store.ErrNotFound) && !os.IsNotExist(e) {
				return false, e
			}
			os.RemoveAll(filepath.Join(m.Store.PublicDir(s.ID), old.LocalID))
			changed = true
		}
	}
	d.Records = keep
	d.CIDs[c.IPNS] = rec.Value
	d.Names[c.IPNS] = c.Name
	// Avatars are only display data and are optional.
	if avatar, err := readFile(tree, "avatar.png", 5<<20); err == nil {
		dir := filepath.Join(m.dir(s.ID), "avatars")
		os.MkdirAll(dir, 0700)
		os.WriteFile(filepath.Join(dir, c.IPNS+".png"), avatar, 0600)
	}
	return changed, nil
}
func safeFile(n string) bool {
	return n != "" && n != "." && n != ".." && filepath.Base(n) == n && !strings.ContainsAny(n, "/\\\x00") && n != "article.json" && n != "planet.json"
}
func readFile(root, rel string, limit int64) ([]byte, error) {
	if !filepath.IsLocal(rel) {
		return nil, errors.New("invalid source path")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("source root is not a directory")
	}
	p := root
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		p = filepath.Join(p, part)
		st, e := os.Lstat(p)
		if e != nil {
			return nil, e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("source contains a symbolic link")
		}
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("source file is not regular")
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("source file is too large")
	}
	return b, e
}

package publish

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

type preparedFixture struct {
	ctx                 context.Context
	hostURL, ipns, base string
	laptop              *ipfs.Embedded
	service             *Publisher
}

func newPreparedFixture(t *testing.T) preparedFixture {
	t.Helper()
	ctx := context.Background()
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	laptop := offlineNode(t)
	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	ipnsName, err := laptop.Keystore().Generate(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	site, err := s.Site(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	site.IPNS = ipnsName
	enableHosting(t, site)
	SetHost(site, srv.URL)
	if err := s.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	lp := &Publisher{Store: s, Node: laptop, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: laptop}}
	if err := lp.Render.Render(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	base, err := laptop.AddDir(ctx, s.PublicDir(fixtureID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := laptop.SignRecord(fixtureID, base, 5); err != nil {
		t.Fatal(err)
	}
	if err := lp.Push(ctx, site, base, 5); err != nil {
		t.Fatal(err)
	}
	return preparedFixture{ctx: ctx, hostURL: srv.URL, ipns: ipnsName, base: base, laptop: laptop, service: newKeylessPublisher(t)}
}

func newKeylessPublisher(t *testing.T) *Publisher {
	t.Helper()
	node := offlineNode(t)
	s := &store.Store{Root: t.TempDir()}
	return &Publisher{Store: s, Node: node, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: node}}
}

func (f preparedFixture) prepare(t *testing.T, id string) PreparedPost {
	t.Helper()
	created := store.FromTime(time.Date(2026, 10, 9, 14, 0, 0, 0, time.UTC))
	image := filepath.Join(t.TempDir(), "screenshot.png")
	if err := os.WriteFile(image, []byte("retained screenshot fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := f.service.PreparePost(f.ctx, f.hostURL, f.ipns, NewPost{ID: id, Created: &created, Title: "Phone screenshot", Content: "A caption", Files: []string{image}, HeroImage: "screenshot.png"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.PostID != id || prepared.Parent != f.base || prepared.Sequence != 6 {
		t.Fatalf("wrong proposal: %+v", prepared)
	}
	return prepared
}

func (f preparedFixture) authorize(t *testing.T, prepared PreparedPost) PostAuthorization {
	t.Helper()
	u, _ := url.Parse(f.hostURL)
	at := time.Now().Unix()
	sig, err := f.laptop.Keystore().Sign(fixtureID, host.PushMessage(u.Hostname(), f.ipns, prepared.CID, prepared.Sequence, at))
	if err != nil {
		t.Fatal(err)
	}
	record, err := f.laptop.SignRecord(fixtureID, prepared.CID, prepared.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	return PostAuthorization{Timestamp: at, Signature: sig, Record: record}
}

// Only the laptop has the root key. A fresh service process can recover the
// serialized preparation and retained files, publish it, then find it after a
// second writer has advanced the host and its own commit receipt was lost.
func TestPreparedPostExternalAuthorizationAndRecovery(t *testing.T) {
	f := newPreparedFixture(t)
	id := store.NewID()
	prepared := f.prepare(t, id)
	entry, err := hostEntry(f.ctx, f.hostURL, f.ipns)
	if err != nil || entry.CID != f.base {
		t.Fatalf("prepare published content: %+v, %v", entry, err)
	}
	if keys, _ := os.ReadDir(f.service.Node.Keystore().Dir); len(keys) != 0 {
		t.Fatal("preparation imported a site key")
	}
	b, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	var recovered PreparedPost
	if err := json.Unmarshal(b, &recovered); err != nil {
		t.Fatal(err)
	}
	restarted := newKeylessPublisher(t)
	posted, err := restarted.CommitPreparedPost(f.ctx, recovered, f.authorize(t, recovered))
	if err != nil {
		t.Fatal(err)
	}
	if posted.CID != prepared.CID || !strings.HasSuffix(posted.URL, "/"+id+"/") {
		t.Fatalf("posted: %+v", posted)
	}
	if keys, _ := os.ReadDir(restarted.Node.Keystore().Dir); len(keys) != 0 {
		t.Fatal("commit imported a site key")
	}
	// The host accepted the update; forget that receipt and advance the head.
	pem, err := f.laptop.Keystore().ExportPEM(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := f.service.Post(f.ctx, f.hostURL, pem, NewPost{Title: "Another author"})
	if err != nil {
		t.Fatal(err)
	}
	found, err := newKeylessPublisher(t).InspectPost(f.ctx, f.hostURL, f.ipns, id)
	if err != nil {
		t.Fatal(err)
	}
	if found == nil || found.CID != advanced.CID || found.URL != posted.URL {
		t.Fatalf("lost receipt not recovered: %+v", found)
	}
	none, err := restarted.InspectPost(f.ctx, f.hostURL, f.ipns, store.NewID())
	if err != nil || none != nil {
		t.Fatalf("absent operation: %+v, %v", none, err)
	}
	duplicate, err := restarted.CommitPreparedPost(f.ctx, recovered, f.authorize(t, recovered))
	if err != nil || duplicate.URL != posted.URL {
		t.Fatalf("retry should recover the existing post: %+v, %v", duplicate, err)
	}
	head, err := restarted.InspectSite(f.ctx, f.hostURL, f.ipns)
	if err != nil {
		t.Fatal(err)
	}
	var posts []render.PublicPost
	if err := json.Unmarshal(head.Site.Raw["articles"], &posts); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, post := range posts {
		if post.ID != id {
			continue
		}
		count++
		if post.Created != store.FromTime(time.Date(2026, 10, 9, 14, 0, 0, 0, time.UTC)) || post.HeroImageFilename == nil || *post.HeroImageFilename != "screenshot.png" {
			t.Fatalf("unstable post fields: %+v", post)
		}
	}
	if count != 1 {
		t.Fatalf("expected one post for the stable operation, got %d", count)
	}
}

func TestPreparedPostRejectsChangedParentAndFiles(t *testing.T) {
	f := newPreparedFixture(t)
	first := f.prepare(t, store.NewID())
	second := f.prepare(t, store.NewID())
	if err := os.WriteFile(filepath.Join(first.ChangedDir, "planet.json"), []byte("tampered staging"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CommitPreparedPost(f.ctx, first, f.authorize(t, first)); err == nil || !strings.Contains(err.Error(), "no longer match") {
		t.Fatalf("changed staging accepted: %v", err)
	}
	if _, err := f.service.CommitPreparedPost(f.ctx, second, f.authorize(t, second)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CommitPreparedPost(f.ctx, first, f.authorize(t, first)); !errors.Is(err, ErrParentChanged) {
		t.Fatalf("stale parent accepted: %v", err)
	}
}

func TestPreparedPostRechecksPublishedStorageAndDestination(t *testing.T) {
	for _, tc := range []struct {
		name, key, value string
		want             error
	}{
		{"hosting disabled", store.StorageKey, store.StorageP2P, ErrHostingDisabled},
		{"destination changed", HostKey, "https://other.example", errHostChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPreparedFixture(t)
			prepared := f.prepare(t, store.NewID())
			b, status, err := httpGet(f.ctx, f.hostURL+"/ipfs/"+f.base+"/planet.json")
			if err != nil || status != 200 {
				t.Fatalf("read metadata: %d, %v", status, err)
			}
			var planet map[string]json.RawMessage
			if err := json.Unmarshal(b, &planet); err != nil {
				t.Fatal(err)
			}
			planet[tc.key], _ = json.Marshal(tc.value)
			b, err = json.Marshal(planet)
			if err != nil {
				t.Fatal(err)
			}
			changed := t.TempDir()
			if err := os.WriteFile(filepath.Join(changed, "planet.json"), b, 0o600); err != nil {
				t.Fatal(err)
			}
			c, err := f.laptop.AddOver(f.ctx, f.base, changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.laptop.SignRecord(fixtureID, c, 6); err != nil {
				t.Fatal(err)
			}
			// Model an owner-signed publication of a new storage/destination
			// choice at the old host, so the pending service must observe it.
			owner := &Publisher{Node: f.laptop}
			if err := owner.pushDir(f.ctx, prepared.Site, fixtureID, c, 6, pushSpec{Parent: f.base, Dir: changed}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.CommitPreparedPost(f.ctx, prepared, f.authorize(t, prepared)); !errors.Is(err, tc.want) {
				t.Fatalf("commit ignored published choice: %v", err)
			}
		})
	}
}

func TestPreparedPostRejectsMismatchedAuthorization(t *testing.T) {
	f := newPreparedFixture(t)
	prepared := f.prepare(t, store.NewID())
	auth := f.authorize(t, prepared)
	wrongCIDRecord, err := f.laptop.SignRecord(fixtureID, f.base, prepared.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	wrongSequenceRecord, err := f.laptop.SignRecord(fixtureID, prepared.CID, prepared.Sequence+1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*PreparedPost, *PostAuthorization)
	}{
		{"expired", func(_ *PreparedPost, a *PostAuthorization) { a.Timestamp -= 601 }},
		{"future clock", func(_ *PreparedPost, a *PostAuthorization) { a.Timestamp += 601 }},
		{"missing signature", func(_ *PreparedPost, a *PostAuthorization) { a.Signature = nil }},
		{"missing record", func(_ *PreparedPost, a *PostAuthorization) { a.Record = nil }},
		{"record CID", func(_ *PreparedPost, a *PostAuthorization) { a.Record = wrongCIDRecord }},
		{"record sequence", func(_ *PreparedPost, a *PostAuthorization) { a.Record = wrongSequenceRecord }},
		{"push CID", func(p *PreparedPost, _ *PostAuthorization) { p.CID = f.base }},
		{"push sequence", func(p *PreparedPost, _ *PostAuthorization) { p.Sequence++ }},
		{"push domain", func(p *PreparedPost, _ *PostAuthorization) {
			copy := *p.Site
			copy.Raw = map[string]json.RawMessage{}
			p.Site = &copy
			SetHost(p.Site, "https://wrong.example")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, a := prepared, auth
			tc.change(&p, &a)
			if _, err := f.service.CommitPreparedPost(f.ctx, p, a); err == nil {
				t.Fatal("accepted mismatched authorization")
			}
		})
	}
	entry, err := hostEntry(f.ctx, f.hostURL, f.ipns)
	if err != nil || entry.CID != f.base {
		t.Fatalf("invalid authorization advanced site: %+v, %v", entry, err)
	}
}

func TestInspectSiteRequiresSignedHead(t *testing.T) {
	f := newPreparedFixture(t)
	upstream, _ := url.Parse(f.hostURL)
	var contentReads atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/ipfs/") || strings.HasPrefix(r.URL.Path, "/v0/host/blocks/") {
			contentReads.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, "/routing/v1/ipns/") {
			w.Write([]byte("forged record"))
			return
		}
		r.URL.Scheme, r.URL.Host = upstream.Scheme, upstream.Host
		r.RequestURI = ""
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		var body json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
	}))
	defer proxy.Close()
	if _, err := f.service.InspectSite(f.ctx, proxy.URL, f.ipns); err == nil {
		t.Fatal("accepted unverified host metadata")
	}
	if contentReads.Load() != 0 {
		t.Fatal("read content before verifying the site's signature")
	}
}

func TestStablePostIdentityCannotOverwrite(t *testing.T) {
	s := &store.Store{Root: t.TempDir()}
	id := store.NewID()
	if _, err := addPost(s, fixtureID, NewPost{ID: id, Title: "Original"}); err != nil {
		t.Fatal(err)
	}
	if _, err := addPost(s, fixtureID, NewPost{ID: id, Title: "Replacement"}); !errors.Is(err, ErrPostExists) {
		t.Fatalf("duplicate identity: %v", err)
	}
	if _, err := addPost(s, fixtureID, NewPost{ID: "../escape", Title: "Bad"}); err == nil {
		t.Fatal("accepted escaping post ID")
	}
	post, err := s.Post(fixtureID, id)
	if err != nil || post.Title != "Original" {
		t.Fatalf("original changed: %+v, %v", post, err)
	}
}

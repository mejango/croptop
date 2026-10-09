package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mejango/croptop/internal/gateway"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

func saveStorage(t *testing.T, st *store.Store, id, mode string) *store.Site {
	t.Helper()
	site, err := st.Site(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := site.SetStorage(mode); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	return site
}

func storageResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Status: fmt.Sprintf("%d %s", code, http.StatusText(code)), Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func storageTransport(t *testing.T, f hostTransport) {
	t.Helper()
	before := http.DefaultTransport
	http.DefaultTransport = f
	t.Cleanup(func() { http.DefaultTransport = before })
}

// Missing storage on existing sites and malformed values must not preserve
// the old implicit-hosting behavior, including the force and background paths.
func TestP2PStoragePublishesWithoutHostRequests(t *testing.T) {
	for _, tc := range []struct {
		name, raw          string
		force, wait, first bool
	}{
		{"new site missing choice", "", false, false, true},
		{"existing site missing choice", "", false, false, false},
		{"explicit P2P waiting", `"p2p"`, false, true, false},
		{"forced P2P background", `"p2p"`, true, false, false},
		{"forced P2P waiting", `"p2p"`, true, true, false},
		{"unknown choice", `"invalid"`, false, true, false},
		{"invalid choice type", `true`, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, st, log := fakePublisher(t)
			p.SkipPrewarm, p.Wait = false, tc.wait
			site, _ := st.Site(fixtureID)
			old := "bafyOLD"
			site.LastPublishedCID, site.IPNSSequence = &old, 2
			if tc.first {
				site.LastPublishedCID, site.IPNSSequence = nil, 0
			}
			if err := st.SaveSite(site); err != nil {
				t.Fatal(err)
			}
			// Keep missing and malformed legacy choices in the stored JSON.
			path := filepath.Join(st.SiteDir(fixtureID), "planet.json")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(b, &raw); err != nil {
				t.Fatal(err)
			}
			delete(raw, store.StorageKey)
			if tc.raw != "" {
				raw[store.StorageKey] = json.RawMessage(tc.raw)
			}
			b, _ = json.Marshal(raw)
			if err := os.WriteFile(path, b, 0o644); err != nil {
				t.Fatal(err)
			}
			var contacted atomic.Int64
			storageTransport(t, func(req *http.Request) (*http.Response, error) {
				contacted.Add(1)
				if req.Body != nil {
					io.Copy(io.Discard, req.Body)
				}
				return storageResponse(200, `{"cid":"bafyHOST","sequence":500}`), nil
			})
			res, err := p.Publish(context.Background(), fixtureID, tc.force)
			if err != nil || res.CID != "bafyFAKE" || res.Sequence != 8 {
				t.Fatalf("P2P publish = %+v, %v", res, err)
			}
			p.ProvideAll(context.Background())
			calls, _ := os.ReadFile(log)
			for _, want := range []string{"name publish", "--sequence=8", "pin add --recursive bafyFAKE"} {
				if !strings.Contains(string(calls), want) {
					t.Errorf("P2P publish did not announce and provide over IPFS: missing %q in %s", want, calls)
				}
			}
			if n := contacted.Load(); n != 0 {
				t.Fatalf("P2P publishing made %d host or gateway requests", n)
			}
		})
	}
}

// A previously loaded hosted snapshot cannot authorize requests after the
// owner opts out, including small lookups and already queued uploads.
func TestStorageOptOutRejectsStaleHostedSnapshots(t *testing.T) {
	p, st, _ := fakePublisher(t)
	stale := saveStorage(t, st, fixtureID, store.StorageHosted)
	old := "bafyOLD"
	stale.LastPublishedCID, stale.IPNSSequence = &old, 2
	if err := st.SaveSite(stale); err != nil {
		t.Fatal(err)
	}
	saveStorage(t, st, fixtureID, store.StorageP2P)
	var contacted atomic.Int64
	storageTransport(t, func(req *http.Request) (*http.Response, error) {
		contacted.Add(1)
		return storageResponse(200, `{"cid":"bafyHOST","sequence":500}`), nil
	})
	ctx := context.Background()
	rec, err := p.latestRecord(ctx, stale)
	if err != nil || rec.Sequence != 7 || rec.Value != "/ipfs/bafyOLD" {
		t.Fatalf("P2P lookup must use the network record: %+v, %v", rec, err)
	}
	if err := p.CatchUp(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	if err := p.Keepalive(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	p.warm(stale, "bafyNEW")
	p.prewarm(ctx, stale, "bafyNEW")
	p.prewarmAll(stale, "bafyNEW")
	for name, call := range map[string]func() error{
		"Push":           func() error { return p.Push(ctx, stale, "bafyNEW", 8) },
		"Claim":          func() error { return p.Claim(ctx, stale, "my-site") },
		"queued version": func() error { return p.pushVersion(ctx, &pushJob{site: stale, cid: "bafyNEW", seq: 8}) },
	} {
		if err := call(); !errors.Is(err, ErrHostingDisabled) {
			t.Errorf("%s after opt-out = %v, want ErrHostingDisabled", name, err)
		}
	}
	p.queuePush(stale, "bafyNEW", 8, nil)
	waitFor(t, "the opted-out upload to leave the queue", func() bool {
		p.pushMu.Lock()
		defer p.pushMu.Unlock()
		return !p.pushing[fixtureID] && p.pending[fixtureID] == nil
	})
	if n := contacted.Load(); n != 0 {
		t.Fatalf("a stale hosted snapshot made %d requests after opt-out", n)
	}
}

func TestQueuedUploadStopsAfterStorageOptOut(t *testing.T) {
	p, st, _ := fakePublisher(t)
	site := saveStorage(t, st, fixtureID, store.StorageHosted)
	started, release := make(chan struct{}), make(chan struct{})
	var contacted atomic.Int64
	storageTransport(t, func(req *http.Request) (*http.Response, error) {
		if contacted.Add(1) == 1 {
			close(started)
			<-release
		}
		if req.Body != nil {
			io.Copy(io.Discard, req.Body)
		}
		return storageResponse(503, "temporarily unavailable"), nil
	})
	p.queuePush(site, "bafyNEW", 8, nil)
	<-started
	saveStorage(t, st, fixtureID, store.StorageP2P)
	close(release)
	waitFor(t, "the disabled upload to stop instead of retrying", func() bool {
		p.pushMu.Lock()
		defer p.pushMu.Unlock()
		return !p.pushing[fixtureID] && p.pending[fixtureID] == nil
	})
	if n := contacted.Load(); n != 1 {
		t.Fatalf("queued upload made %d requests; only its pre-opt-out request was allowed", n)
	}
}

func TestStorageOptOutStopsMultipartAndChunkUploads(t *testing.T) {
	batch, chunk := pushBatch, pushChunk
	pushBatch, pushChunk = 64, 32
	t.Cleanup(func() { pushBatch, pushChunk = batch, chunk })
	for _, kind := range []string{"multipart", "chunks"} {
		t.Run(kind, func(t *testing.T) {
			p, st, _ := fakePublisher(t)
			site := saveStorage(t, st, fixtureID, store.StorageHosted)
			var uploads atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodPost {
					http.NotFound(w, req)
					return
				}
				io.Copy(io.Discard, req.Body)
				if uploads.Add(1) == 1 {
					if kind == "chunks" && req.Header.Get("X-Croptop-Chunk") != "1/5" {
						t.Errorf("first upload was not the expected chunk: %s", req.Header.Get("X-Croptop-Chunk"))
					}
					if kind == "multipart" && req.Header.Get("X-Croptop-Part") != "1/2" {
						t.Errorf("first upload was not the expected part: %s", req.Header.Get("X-Croptop-Part"))
					}
					saveStorage(t, st, fixtureID, store.StorageP2P)
				}
				io.WriteString(w, `{"upload":"first"}`)
			}))
			defer srv.Close()
			SetHost(site, srv.URL)
			if err := st.SaveSite(site); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			files := map[string]int{"a.html": 40, "b.html": 40}
			if kind == "chunks" {
				files = map[string]int{"index.html": 2, "video.mp4": 129}
			}
			for name, size := range files {
				if err := os.WriteFile(filepath.Join(dir, name), bytes.Repeat([]byte("x"), size), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err := p.pushDir(context.Background(), site, fixtureID, "bafyNEW", 8, pushSpec{Dir: dir})
			if !errors.Is(err, ErrHostingDisabled) || uploads.Load() != 1 {
				t.Fatalf("upload after first %s request = %v, requests = %d", kind, err, uploads.Load())
			}
		})
	}
}

func TestHostedRequestsUseTheSavedHost(t *testing.T) {
	p, st, _ := fakePublisher(t)
	stale := saveStorage(t, st, fixtureID, store.StorageHosted)
	SetHost(stale, "https://stale.example")
	if err := st.SaveSite(stale); err != nil {
		t.Fatal(err)
	}
	current, _ := st.Site(fixtureID)
	SetHost(current, "https://current.example")
	if err := st.SaveSite(current); err != nil {
		t.Fatal(err)
	}
	var uploads, claims int
	storageTransport(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "current.example" {
			t.Errorf("stale snapshot contacted %s", req.URL.Host)
		}
		if req.Body != nil {
			io.Copy(io.Discard, req.Body)
		}
		switch req.URL.Path {
		case "/v0/host/push":
			uploads++
		case "/v0/host/names":
			claims++
		default:
			return storageResponse(404, "not held"), nil
		}
		return storageResponse(200, `{}`), nil
	})
	if err := p.Render.Render(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	if err := p.Push(context.Background(), stale, "bafyNEW", 8); err != nil {
		t.Fatal(err)
	}
	if err := p.Claim(context.Background(), stale, "my-site"); err != nil {
		t.Fatal(err)
	}
	current, _ = st.Site(fixtureID)
	if uploads == 0 || claims != 1 || HostOf(current) != "https://current.example" || !current.HostingEnabled() {
		t.Fatalf("saved host was not preserved: uploads %d, claims %d, host %s, mode %s", uploads, claims, HostOf(current), current.StorageMode())
	}
}

func TestKeyPostHonorsPublishedStorageAndUsesItsStagedStore(t *testing.T) {
	for _, mode := range []string{store.StorageP2P, store.StorageHosted} {
		t.Run(mode, func(t *testing.T) {
			r := newChangesRig(t)
			ctx := context.Background()
			if mode == store.StorageP2P {
				// Stage a real published P2P snapshot. The upload itself is
				// authorized by the current owner store, not this snapshot.
				saveStorage(t, r.store, fixtureID, store.StorageP2P)
				if err := r.p.Render.Render(ctx, fixtureID); err != nil {
					t.Fatal(err)
				}
				cid, err := r.laptop.AddDir(ctx, r.store.PublicDir(fixtureID))
				if err != nil {
					t.Fatal(err)
				}
				site := saveStorage(t, r.store, fixtureID, store.StorageHosted)
				if err := r.p.Push(ctx, site, cid, site.IPNSSequence+1); err != nil {
					t.Fatal(err)
				}
			}
			pem, err := r.laptop.Keystore().ExportPEM(fixtureID)
			if err != nil {
				t.Fatal(err)
			}
			node, agentStore := offlineNode(t), &store.Store{Root: t.TempDir()}
			ap := &Publisher{Store: agentStore, Node: node, Render: &render.Renderer{Store: agentStore, Templates: templates.FS, CIDs: node}}
			original := http.DefaultTransport
			var uploads atomic.Int64
			storageTransport(t, func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost && req.URL.Path == "/v0/host/push" {
					uploads.Add(1)
				}
				return original.RoundTrip(req)
			})
			posted, err := ap.Post(ctx, r.srv.URL, pem, NewPost{Title: "storage-aware agent post"})
			if mode == store.StorageP2P {
				if !errors.Is(err, ErrHostingDisabled) || uploads.Load() != 0 {
					t.Fatalf("key post to P2P snapshot = %+v, %v; uploads %d", posted, err, uploads.Load())
				}
			} else if err != nil || posted.CID == "" || uploads.Load() == 0 {
				t.Fatalf("hosted key post with empty owner store = %+v, %v; uploads %d", posted, err, uploads.Load())
			}
			if entries, _ := os.ReadDir(agentStore.Root); len(entries) != 0 {
				t.Errorf("the staged store was not removed: %v", entries)
			}
		})
	}
}

type storageSparseNode struct {
	*noPeers
	gets atomic.Int64
}

func (n *storageSparseNode) Get(ctx context.Context, path, dest string) error {
	n.gets.Add(1)
	return n.noPeers.Get(ctx, path, dest)
}

// Leave a newer version's blocks in the IPFS node but remove its post from
// this machine's source, as if a second machine had produced the version.
func sparseStorageVersion(t *testing.T) (*changesRig, *ipfs.Record, *store.Post, *storageSparseNode) {
	t.Helper()
	r := newChangesRig(t)
	attachment := filepath.Join(t.TempDir(), "peer.txt")
	if err := os.WriteFile(attachment, []byte("attachment available from IPFS"), 0o644); err != nil {
		t.Fatal(err)
	}
	post, err := addPost(r.store, fixtureID, NewPost{Title: "A post from another peer", Files: []string{attachment}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.p.Render.Render(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	cid, err := r.laptop.AddDir(context.Background(), r.store.PublicDir(fixtureID))
	if err != nil {
		t.Fatal(err)
	}
	site, _ := r.store.Site(fixtureID)
	rec := &ipfs.Record{Value: "/ipfs/" + cid, Sequence: site.IPNSSequence + 1}
	if err := r.laptop.NamePublish(context.Background(), fixtureID, cid, rec.Sequence); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(r.store.ArticlesDir(fixtureID), post.ID+".json")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(r.store.PostDir(fixtureID, post.ID)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(r.store.PublicDir(fixtureID), post.ID)); err != nil {
		t.Fatal(err)
	}
	node := &storageSparseNode{noPeers: r.laptop}
	r.p.Node = node
	return r, rec, post, node
}

func assertSparseStoragePost(t *testing.T, r *changesRig, post *store.Post, node *storageSparseNode) {
	t.Helper()
	if got, err := r.store.Post(fixtureID, post.ID); err != nil || got.Title != post.Title {
		t.Fatalf("the peer's post was not merged: %+v, %v", got, err)
	}
	b, err := os.ReadFile(filepath.Join(r.store.PostDir(fixtureID, post.ID), "peer.txt"))
	if err != nil || string(b) != "attachment available from IPFS" {
		t.Fatalf("the peer's attachment was not fetched over IPFS: %q, %v", b, err)
	}
	if node.gets.Load() < 3 {
		t.Fatalf("sparse sync fetched only %d files through IPFS", node.gets.Load())
	}
	site, _ := r.store.Site(fixtureID)
	if site.HostingEnabled() {
		t.Fatal("taking in a hosted peer's version opted the local site into hosting")
	}
}

func TestP2PStorageSparseSyncUsesIPFS(t *testing.T) {
	r, rec, post, node := sparseStorageVersion(t)
	saveStorage(t, r.store, fixtureID, store.StorageP2P)
	var contacted atomic.Int64
	storageTransport(t, func(req *http.Request) (*http.Response, error) {
		contacted.Add(1)
		return storageResponse(503, "host must not be used"), nil
	})
	result, err := r.p.Sync(context.Background(), fixtureID)
	if err != nil || result.Added != 1 || result.Updated != 0 || result.Result.Sequence <= rec.Sequence {
		t.Fatalf("P2P sparse sync = %+v, %v", result, err)
	}
	assertSparseStoragePost(t, r, post, node)
	if n := contacted.Load(); n != 0 {
		t.Fatalf("P2P sparse sync made %d HTTP requests", n)
	}
}

func TestStorageOptOutDuringSparsePullSwitchesToIPFS(t *testing.T) {
	r, rec, post, node := sparseStorageVersion(t)
	site, _ := r.store.Site(fixtureID)
	var contacted atomic.Int64
	storageTransport(t, func(req *http.Request) (*http.Response, error) {
		if contacted.Add(1) == 1 {
			saveStorage(t, r.store, fixtureID, store.StorageP2P)
		}
		return storageResponse(404, "fetch this version from its peer"), nil
	})
	added, updated, err := r.p.pull(context.Background(), site, rec)
	if err != nil || added != 1 || updated != 0 {
		t.Fatalf("sparse pull while opting out: added %d, updated %d, %v", added, updated, err)
	}
	assertSparseStoragePost(t, r, post, node)
	if n := contacted.Load(); n != 1 {
		t.Fatalf("sparse pull made %d requests; only the first was authorized before opt-out", n)
	}
}

type storageAdoptNode struct {
	*noPeers
	name, cid, published string
	sequence             uint64
}

func (n *storageAdoptNode) Resolve(ctx context.Context, path string) (string, error) {
	if path != "/ipns/"+n.name {
		return "", fmt.Errorf("unexpected name %s", path)
	}
	return "/ipfs/" + n.cid, nil
}

func (n *storageAdoptNode) Get(ctx context.Context, path, dest string) error {
	if path != "/ipfs/"+n.cid {
		return fmt.Errorf("unexpected published tree %s", path)
	}
	return os.CopyFS(dest, os.DirFS(n.published))
}

func (n *storageAdoptNode) NetworkRecord(ctx context.Context, name string) (*ipfs.Record, error) {
	if name != n.name {
		return nil, fmt.Errorf("unexpected record %s", name)
	}
	return &ipfs.Record{Value: "/ipfs/" + n.cid, Sequence: n.sequence}, nil
}

func TestAdoptStorageConsentBelongsToTheLocalOwner(t *testing.T) {
	source := newChangesRig(t)
	remote, err := source.store.Site(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	pem, err := source.laptop.Keystore().ExportPEM(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	published := t.TempDir()
	if err := os.CopyFS(published, os.DirFS(source.store.PublicDir(fixtureID))); err != nil {
		t.Fatal(err)
	}
	// The published source claims hosting and carries preferences belonging
	// to another machine. Neither is this owner's authorization to upload.
	path := filepath.Join(published, "planet.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(b, &metadata); err != nil {
		t.Fatal(err)
	}
	metadata[store.StorageKey] = json.RawMessage(`"hosted"`)
	metadata[HostKey] = json.RawMessage(`"https://remote.example"`)
	metadata[NameKey] = json.RawMessage(`"remote-name"`)
	metadata[gateway.SettingKey] = json.RawMessage(`"crop.top"`)
	b, _ = json.Marshal(metadata)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mode    string
		force, legacy bool
	}{
		{"new copy", store.StorageP2P, false, false},
		{"forced P2P copy", store.StorageP2P, true, false},
		{"forced hosted copy", store.StorageHosted, true, false},
		{"forced legacy copy", store.StorageP2P, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &store.Store{Root: t.TempDir()}
			if tc.force {
				if err := os.CopyFS(st.SiteDir(fixtureID), os.DirFS(source.store.SiteDir(fixtureID))); err != nil {
					t.Fatal(err)
				}
				local := saveStorage(t, st, fixtureID, tc.mode)
				SetHost(local, "https://owner.example")
				setRaw(local, NameKey, "owner-name")
				gateway.Set(local, "shop")
				if err := st.SaveSite(local); err != nil {
					t.Fatal(err)
				}
				if tc.legacy {
					path := filepath.Join(st.SiteDir(fixtureID), "planet.json")
					b, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var raw map[string]json.RawMessage
					if err := json.Unmarshal(b, &raw); err != nil {
						t.Fatal(err)
					}
					delete(raw, store.StorageKey)
					b, _ = json.Marshal(raw)
					if err := os.WriteFile(path, b, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			node := &storageAdoptNode{noPeers: &noPeers{Embedded: offlineNode(t)}, name: source.ipns, cid: *remote.LastPublishedCID, published: published, sequence: remote.IPNSSequence}
			p := &Publisher{Store: st, Node: node, Render: &render.Renderer{Store: st, Templates: templates.FS, CIDs: node}, SkipPrewarm: true}
			var contacted, uploads atomic.Int64
			storageTransport(t, func(req *http.Request) (*http.Response, error) {
				contacted.Add(1)
				if req.URL.Host != "owner.example" {
					t.Errorf("adopt inherited another machine's endpoint %s", req.URL.Host)
				}
				if req.Body != nil {
					io.Copy(io.Discard, req.Body)
				}
				if req.Method == http.MethodPost && req.URL.Path == "/v0/host/push" {
					uploads.Add(1)
					return storageResponse(200, `{}`), nil
				}
				return storageResponse(404, "not held"), nil
			})
			id, err := p.Adopt(context.Background(), source.ipns, pem, tc.force)
			if err != nil || id != fixtureID || !node.Keystore().Has(fixtureID) {
				t.Fatalf("adopt = %s, %v", id, err)
			}
			adopted, err := st.Site(fixtureID)
			if err != nil || adopted.StorageMode() != tc.mode {
				t.Fatalf("adopt did not retain local consent: %+v, %v", adopted, err)
			}
			if tc.force {
				if HostOf(adopted) != "https://owner.example" || NameOf(adopted) != "owner-name" || rawString(adopted, gateway.SettingKey) != "shop" {
					t.Fatalf("forced adopt lost local preferences: host %s, name %s, gateway %s", HostOf(adopted), NameOf(adopted), rawString(adopted, gateway.SettingKey))
				}
			} else {
				for _, key := range []string{HostKey, NameKey} {
					if _, ok := adopted.Raw[key]; ok {
						t.Errorf("new adopted copy retained remote private preference %s", key)
					}
				}
				if _, err := p.Publish(context.Background(), fixtureID, false); err != nil {
					t.Fatalf("publishing a newly adopted P2P copy: %v", err)
				}
			}
			if tc.mode == store.StorageHosted {
				if err := p.Render.Render(context.Background(), fixtureID); err != nil {
					t.Fatal(err)
				}
				cid, err := node.AddDir(context.Background(), st.PublicDir(fixtureID))
				if err != nil {
					t.Fatal(err)
				}
				if err := p.Push(context.Background(), adopted, cid, adopted.IPNSSequence+1); err != nil || uploads.Load() == 0 {
					t.Fatalf("hosted adopted copy did not use its saved host: %v, uploads %d", err, uploads.Load())
				}
			} else {
				if err := p.Push(context.Background(), adopted, "bafyNEW", adopted.IPNSSequence+1); !errors.Is(err, ErrHostingDisabled) {
					t.Fatalf("adopted P2P copy allowed hosted upload: %v", err)
				}
				if n := contacted.Load(); n != 0 {
					t.Fatalf("adopted P2P copy made %d HTTP requests", n)
				}
			}
		})
	}
}

// Embedding only Engine gives this wrapper the same full-tree fetch path
// as kubo, without the embedded engine's sparse-reading capabilities.
type storageFullTreeNode struct {
	ipfs.Engine
	get func(context.Context, string, string) error
}

func (n *storageFullTreeNode) Get(ctx context.Context, path, dest string) error {
	return n.get(ctx, path, dest)
}

func TestStorageOptOutDuringFullTreePullRechecksGatewayRequests(t *testing.T) {
	for _, phase := range []string{"while IPFS is reading", "after the first host request"} {
		t.Run(phase, func(t *testing.T) {
			p, st, _ := fakePublisher(t)
			site := saveStorage(t, st, fixtureID, store.StorageHosted)
			started, release := make(chan struct{}), make(chan struct{})
			p.Node = &storageFullTreeNode{Engine: p.Node, get: func(ctx context.Context, path, dest string) error {
				if phase == "while IPFS is reading" {
					close(started)
					<-release
				}
				return errors.New("no IPFS peer available")
			}}
			var external, cropRequests atomic.Int64
			body := fmt.Sprintf(`{"id":%q,"ipns":%q,"name":"Peer copy","croptopStorage":"hosted","articles":[]}`, fixtureID, site.IPNS)
			storageTransport(t, func(req *http.Request) (*http.Response, error) {
				isCrop := req.URL.Host == "crop.top" || strings.HasSuffix(req.URL.Host, ".crop.top")
				if isCrop {
					if cropRequests.Add(1) == 1 && phase == "after the first host request" {
						saveStorage(t, st, fixtureID, store.StorageP2P)
					}
				} else {
					external.Add(1)
					if phase == "after the first host request" {
						return storageResponse(503, "not on this gateway"), nil
					}
				}
				if strings.HasSuffix(req.URL.Path, "/planet.json") {
					return storageResponse(200, body), nil
				}
				return storageResponse(404, "optional file not present"), nil
			})
			type pulled struct {
				added, updated int
				err            error
			}
			done := make(chan pulled, 1)
			go func() {
				a, u, err := p.pull(context.Background(), site, &ipfs.Record{Value: "/ipfs/bafyFAKE", Sequence: 8})
				done <- pulled{a, u, err}
			}()
			if phase == "while IPFS is reading" {
				<-started
				saveStorage(t, st, fixtureID, store.StorageP2P)
				close(release)
			}
			result := <-done
			if result.err != nil || result.added != 0 || result.updated != 0 {
				t.Fatalf("full-tree pull during opt-out = %+v", result)
			}
			wantCrop := int64(0)
			if phase == "after the first host request" {
				wantCrop = 1
			}
			if got := cropRequests.Load(); got != wantCrop || external.Load() == 0 {
				t.Fatalf("full-tree pull contacted crop.top %d times (want %d) and external gateways %d times", got, wantCrop, external.Load())
			}
			current, _ := st.Site(fixtureID)
			if current.HostingEnabled() {
				t.Fatal("full-tree pull restored the remote hosting choice over the local opt-out")
			}
		})
	}
}

func TestImportPlanetStorageConsentBelongsToTheLocalOwner(t *testing.T) {
	container := t.TempDir()
	my := filepath.Join(container, "Documents", "Planet", "My", fixtureID)
	if err := os.CopyFS(my, os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	sourceKeys := &ipfs.Keystore{Dir: filepath.Join(container, "Library", "Application Support", "ipfs", "keystore")}
	wantName, err := sourceKeys.Generate(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(my, "planet.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var source store.Site
	if err := json.Unmarshal(b, &source); err != nil {
		t.Fatal(err)
	}
	source.IPNS = wantName
	if err := source.SetStorage(store.StorageHosted); err != nil {
		t.Fatal(err)
	}
	SetHost(&source, "https://native-source.example")
	setRaw(&source, NameKey, "native-name")
	gateway.Set(&source, "crop.top")
	b, _ = json.Marshal(source)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	public := filepath.Join(container, "Documents", "Planet", "Public", fixtureID)
	if err := os.MkdirAll(public, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("native public index"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mode    string
		force, legacy bool
	}{
		{"new hosted native source", store.StorageP2P, false, false},
		{"forced P2P native import", store.StorageP2P, true, false},
		{"forced hosted native import", store.StorageHosted, true, false},
		{"forced legacy native import", store.StorageP2P, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &store.Store{Root: t.TempDir()}
			if tc.force {
				if err := os.CopyFS(st.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
					t.Fatal(err)
				}
				local := saveStorage(t, st, fixtureID, tc.mode)
				SetHost(local, "https://owner.example")
				setRaw(local, NameKey, "owner-name")
				gateway.Set(local, "shop")
				if tc.legacy {
					delete(local.Raw, store.StorageKey)
				}
				if err := st.SaveSite(local); err != nil {
					t.Fatal(err)
				}
			}
			script, err := filepath.Abs("testdata/fake-ipfs")
			if err != nil {
				t.Fatal(err)
			}
			node := ipfs.NewNode(script, filepath.Join(st.Root, "ipfs"))
			ids, err := ImportPlanet(st, node.Keystore(), container, tc.force, nil)
			if err != nil || len(ids) != 1 || ids[0] != fixtureID {
				t.Fatalf("native import = %v, %v", ids, err)
			}
			if got, err := node.Keystore().Name(fixtureID); err != nil || got != wantName {
				t.Fatalf("native key was not imported: %s, %v", got, err)
			}
			imported, err := st.Site(fixtureID)
			if err != nil || imported.StorageMode() != tc.mode {
				t.Fatalf("native import lost local storage choice: %+v, %v", imported, err)
			}
			wantHost, wantClaim, wantGateway := "https://native-source.example", "native-name", "crop.top"
			if tc.force {
				wantHost, wantClaim, wantGateway = "https://owner.example", "owner-name", "shop"
			}
			if HostOf(imported) != wantHost || NameOf(imported) != wantClaim || rawString(imported, gateway.SettingKey) != wantGateway {
				t.Fatalf("native import changed endpoint preferences: host %s, name %s, gateway %s", HostOf(imported), NameOf(imported), rawString(imported, gateway.SettingKey))
			}
			var contacted, uploads atomic.Int64
			storageTransport(t, func(req *http.Request) (*http.Response, error) {
				contacted.Add(1)
				if req.URL.Host != "owner.example" {
					t.Errorf("forced import contacted source endpoint %s", req.URL.Host)
				}
				if req.Body != nil {
					io.Copy(io.Discard, req.Body)
				}
				if req.Method == http.MethodPost {
					uploads.Add(1)
					return storageResponse(200, `{}`), nil
				}
				return storageResponse(404, "not held"), nil
			})
			p := &Publisher{Store: st, Node: node}
			err = p.Push(context.Background(), imported, "bafyNEW", 1)
			if tc.mode == store.StorageHosted {
				if err != nil || uploads.Load() == 0 {
					t.Fatalf("hosted native import did not upload to its current host: %v, uploads %d", err, uploads.Load())
				}
			} else if !errors.Is(err, ErrHostingDisabled) || contacted.Load() != 0 {
				t.Fatalf("P2P native import contacted host: %v, requests %d", err, contacted.Load())
			}
		})
	}
}

package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/store"
)

// The host announces its committed record. Preparing a phone must never wait
// for either desktop announcement API, including when its error is ignored.
type hostedNoAnnounce struct {
	*noPeers
	calls        atomic.Int32
	failManifest bool
}

func (n *hostedNoAnnounce) NamePublish(context.Context, string, string, uint64) error {
	n.calls.Add(1)
	return errors.New("desktop announcement is unavailable")
}

func (n *hostedNoAnnounce) AnnounceRecord(context.Context, string, string, []byte) error {
	n.calls.Add(1)
	return errors.New("desktop announcement is unavailable")
}

func (n *hostedNoAnnounce) CheckManifest(ctx context.Context, root, parent string, upload, carry []string) error {
	if n.failManifest {
		return errors.New("injected local manifest comparison failure")
	}
	return n.noPeers.CheckManifest(ctx, root, parent, upload, carry)
}

func hostedGuardAnnouncements(t *testing.T, r *changesRig) *hostedNoAnnounce {
	t.Helper()
	n := &hostedNoAnnounce{noPeers: r.laptop}
	r.p.Node = n
	t.Cleanup(func() {
		if calls := n.calls.Load(); calls != 0 {
			t.Errorf("phone preparation called desktop announcement %d times", calls)
		}
	})
	return n
}

type hostedNetworkRecord struct {
	*hostedNoAnnounce
	record *ipfs.Record
	err    error
}

func (n *hostedNetworkRecord) NetworkRecord(context.Context, string) (*ipfs.Record, error) {
	return n.record, n.err
}

func hostedAssertNoQueue(t *testing.T, p *Publisher) {
	t.Helper()
	p.pushMu.Lock()
	defer p.pushMu.Unlock()
	if len(p.pending) != 0 || len(p.pushing) != 0 || len(p.wake) != 0 {
		t.Fatalf("phone preparation left background uploads: pending=%d pushing=%d wake=%d", len(p.pending), len(p.pushing), len(p.wake))
	}
}

func hostedCheckWholeManifestRequests(t *testing.T, r *changesRig) *atomic.Int32 {
	t.Helper()
	transport := http.DefaultTransport
	checked := &atomic.Int32{}
	storageTransport(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost && req.URL.String() == r.srv.URL+"/v0/host/push" {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			req.Body.Close()
			req.Body = io.NopCloser(bytes.NewReader(body))
			_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
			if err != nil {
				return nil, err
			}
			parts := multipart.NewReader(bytes.NewReader(body), params["boundary"])
			for {
				part, err := parts.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, err
				}
				if part.FormName() == "manifest" {
					var manifest struct {
						Carry []string `json:"carry"`
					}
					if err := json.NewDecoder(part).Decode(&manifest); err != nil {
						return nil, err
					}
					if req.Header.Get("X-Croptop-Parent") == "" || manifest.Carry == nil || len(manifest.Carry) != 0 {
						t.Errorf("whole fallback must keep a parent and explicit empty carry: parent=%q carry=%v", req.Header.Get("X-Croptop-Parent"), manifest.Carry)
					}
					checked.Add(1)
				}
				part.Close()
			}
		}
		return transport.RoundTrip(req)
	})
	return checked
}

// A legacy public P2P descriptor can already be on the host. The approved
// bootstrap publishes hosted metadata and the actual large attachment before
// claiming that setup is ready, despite the ordinary 16 MiB sync threshold.
func TestPublishHostedCommitsLargeLegacyP2PMigration(t *testing.T) {
	ctx := context.Background()
	r := newChangesRig(t)
	saveStorage(t, r.store, fixtureID, store.StorageP2P)
	if err := r.p.Render.Render(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	legacyCID, err := r.laptop.AddDir(ctx, r.store.PublicDir(fixtureID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.laptop.SignRecord(fixtureID, legacyCID, 2); err != nil {
		t.Fatal(err)
	}
	site := saveStorage(t, r.store, fixtureID, store.StorageHosted)
	if err := r.p.Push(ctx, site, legacyCID, 2); err != nil {
		t.Fatal(err)
	}
	if err := r.p.published(fixtureID, legacyCID, 2); err != nil {
		t.Fatal(err)
	}
	before, err := r.p.InspectSite(ctx, r.srv.URL, r.ipns)
	if err != nil || before.Site.HostingEnabled() {
		t.Fatalf("legacy fixture must publish P2P metadata: %+v, %v", before, err)
	}
	const name = "large-phone-bootstrap.bin"
	const size = 17 << 20
	if err := os.MkdirAll(r.store.PostDir(fixtureID, r.post.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.store.PostDir(fixtureID, r.post.ID), name), bytes.Repeat([]byte{0x5a}, size), 0o600); err != nil {
		t.Fatal(err)
	}
	r.post.Attachments = append(r.post.Attachments, name)
	if err := r.store.SavePost(fixtureID, r.post); err != nil {
		t.Fatal(err)
	}
	hostedGuardAnnouncements(t, r)
	r.reset()
	var stages []HostedStage
	res, err := r.p.PublishHosted(ctx, fixtureID, func(stage HostedStage) { stages = append(stages, stage) })
	if err != nil {
		t.Fatal(err)
	}
	if sent, _ := r.counts(); sent < size {
		t.Fatalf("returned before the large upload: sent %d, want at least %d", sent, size)
	}
	after, err := r.p.InspectSite(ctx, r.srv.URL, r.ipns)
	if err != nil || after.CID != res.CID || after.Sequence != res.Sequence || !after.Site.HostingEnabled() {
		t.Fatalf("returned without a verified hosted head: %+v, %v; result %+v", after, err, res)
	}
	b, status, err := httpGet(ctx, r.srv.URL+"/ipfs/"+res.CID+"/"+r.post.ID+"/"+name)
	if err != nil || status != 200 || len(b) != size || !bytes.Equal(b, bytes.Repeat([]byte{0x5a}, size)) {
		t.Fatalf("host did not retain the complete attachment: status=%d bytes=%d err=%v", status, len(b), err)
	}
	if want := []HostedStage{HostedRendering, HostedChecking, HostedUploading, HostedVerifying}; !reflect.DeepEqual(stages, want) {
		t.Fatalf("progress=%v, want %v", stages, want)
	}
	hostedAssertNoQueue(t, r.p)
}

func TestPublishHostedPreservesConcurrentLowerSequencePost(t *testing.T) {
	r := newChangesRig(t)
	post := r.agent()
	if err := r.p.saveSite(fixtureID, func(site *store.Site) { site.IPNSSequence = 40 }); err != nil {
		t.Fatal(err)
	}
	r.edit("\n\nphone bootstrap edit")
	r.beforeManifest = func() { post("Concurrent lower-sequence phone post") }
	n := hostedGuardAnnouncements(t, r)
	n.failManifest = true // Exercise whole-files fallback, not the delta fast path.
	checked := hostedCheckWholeManifestRequests(t, r)
	r.reset()
	res, err := r.p.PublishHosted(context.Background(), fixtureID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sequence <= 40 {
		t.Fatalf("sequence went below the local renewal counter: %+v", res)
	}
	if _, manifests := r.counts(); manifests < 2 {
		t.Fatalf("concurrent head did not trigger a parent-checked retry: %d", manifests)
	}
	if checked.Load() < 2 {
		t.Fatalf("did not verify both full-manifest attempts: %d", checked.Load())
	}
	r.keeps(res, "Concurrent lower-sequence phone post", "phone bootstrap edit")
	hostedAssertNoQueue(t, r.p)
}

func TestPublishHostedRecoversLostCommitResponse(t *testing.T) {
	r := newChangesRig(t)
	r.edit("\n\ncommitted even when the answer disappears")
	hostedGuardAnnouncements(t, r)
	transport := http.DefaultTransport
	var lost atomic.Bool
	storageTransport(t, func(req *http.Request) (*http.Response, error) {
		resp, err := transport.RoundTrip(req)
		if err == nil && req.Method == http.MethodPost && req.URL.String() == r.srv.URL+"/v0/host/push" && resp.StatusCode == 200 && lost.CompareAndSwap(false, true) {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return nil, errors.New("simulated connection loss after host commit")
		}
		return resp, err
	})
	res, err := r.p.PublishHosted(context.Background(), fixtureID, nil)
	if err != nil || !lost.Load() {
		t.Fatalf("lost committed answer was not recovered: %+v, %v; lost=%v", res, err, lost.Load())
	}
	entry, err := r.entry()
	if err != nil || entry.CID != res.CID || entry.Sequence != res.Sequence {
		t.Fatalf("recovery did not return the committed head: %+v, %v; result=%+v", entry, err, res)
	}
	hostedAssertNoQueue(t, r.p)
}

func TestPublishHostedUnchangedContentUsesActualHostSequence(t *testing.T) {
	r := newChangesRig(t)
	entry, err := r.entry()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.p.saveSite(fixtureID, func(site *store.Site) { site.IPNSSequence = 40 }); err != nil {
		t.Fatal(err)
	}
	hostedGuardAnnouncements(t, r)
	r.reset()
	res, err := r.p.PublishHosted(context.Background(), fixtureID, nil)
	if err != nil || res.CID != entry.CID || res.Sequence != entry.Sequence {
		t.Fatalf("unchanged head=%+v, result=%+v, err=%v", entry, res, err)
	}
	site, err := r.store.Site(fixtureID)
	if err != nil || site.IPNSSequence != 40 {
		t.Fatalf("local renewal counter was lowered: %+v, %v", site, err)
	}
	if sent, _ := r.counts(); sent != 0 {
		t.Fatalf("unchanged content was reuploaded: %d bytes", sent)
	}
	hostedAssertNoQueue(t, r.p)
}

func TestPublishHostedCancelDoesNotQueueOrClaimCommit(t *testing.T) {
	r := newChangesRig(t)
	entry, err := r.entry()
	if err != nil {
		t.Fatal(err)
	}
	r.edit("\n\nnot uploaded after cancel")
	hostedGuardAnnouncements(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.reset()
	res, err := r.p.PublishHosted(ctx, fixtureID, func(stage HostedStage) {
		if stage == HostedUploading {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || res.CID != "" {
		t.Fatalf("canceled setup claimed success: %+v, %v", res, err)
	}
	if sent, _ := r.counts(); sent != 0 {
		t.Fatalf("canceled setup sent %d bytes", sent)
	}
	after, err := r.entry()
	if err != nil || after.CID != entry.CID {
		t.Fatalf("canceled setup replaced the host: %+v, %v", after, err)
	}
	hostedAssertNoQueue(t, r.p)
	if _, err := r.p.PublishHosted(context.Background(), fixtureID, nil); err != nil {
		t.Fatalf("canceled setup retained the lock: %v", err)
	}
}

func TestPublishHostedLockWaitHonorsDeadline(t *testing.T) {
	p := &Publisher{}
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := p.PublishHosted(ctx, fixtureID, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("lock wait ignored the deadline: %v after %v", err, time.Since(start))
	}
}

func TestPublishHostedRequiresSavedHostingConsent(t *testing.T) {
	r := newChangesRig(t)
	saveStorage(t, r.store, fixtureID, store.StorageP2P)
	hostedGuardAnnouncements(t, r)
	r.reset()
	_, err := r.p.PublishHosted(context.Background(), fixtureID, nil)
	if !errors.Is(err, ErrHostingDisabled) {
		t.Fatalf("P2P site was accepted: %v", err)
	}
	if sent, _ := r.counts(); sent != 0 {
		t.Fatalf("P2P site uploaded %d bytes", sent)
	}
	hostedAssertNoQueue(t, r.p)
}

func TestPublishHostedMissingExistingHeadFailsBeforeUpload(t *testing.T) {
	r := newChangesRig(t)
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	var pushes atomic.Int32
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/v0/host/") {
			pushes.Add(1)
		}
		h.ServeHTTP(w, req)
	}))
	t.Cleanup(empty.Close)
	if err := r.p.saveSite(fixtureID, func(site *store.Site) { SetHost(site, empty.URL) }); err != nil {
		t.Fatal(err)
	}
	hostedGuardAnnouncements(t, r)
	_, err := r.p.PublishHosted(context.Background(), fixtureID, nil)
	if !errors.Is(err, ErrHostedBootstrapNeedsPublish) || pushes.Load() != 0 {
		t.Fatalf("missing-head setup risked overwriting a competing first publication: %v, pushes=%d", err, pushes.Load())
	}
	hostedAssertNoQueue(t, r.p)
}

func TestPublishHostedFirstHeadRequiresKnownFreshIdentity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		record      *ipfs.Record
		err         error
		wantSuccess bool
	}{
		{name: "explicitly absent network record", err: ipfs.ErrNoRecord, wantSuccess: true},
		{name: "existing network record without local history", record: &ipfs.Record{Value: "/ipfs/bafyexisting", Sequence: 17}},
		{name: "network lookup unavailable", err: errors.New("DHT temporarily unreachable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newChangesRig(t)
			// A new identity has no entry at the real host, and no local publish
			// history. Keep the unrelated old identity there to exercise lookup.
			r.laptop = &noPeers{Embedded: offlineNode(t)}
			name, err := r.laptop.Keystore().Generate(fixtureID)
			if err != nil {
				t.Fatal(err)
			}
			r.ipns = name
			r.p.Render.CIDs = r.laptop
			if err := r.p.saveSite(fixtureID, func(site *store.Site) {
				site.IPNS = name
				site.LastPublishedCID, site.LastPublished = nil, nil
				site.IPNSSequence, site.PublishedElsewhere = 0, false
				delete(site.Raw, VersionsKey)
				delete(site.Raw, "lastPublishedCID")
				delete(site.Raw, "lastPublished")
			}); err != nil {
				t.Fatal(err)
			}
			n := hostedGuardAnnouncements(t, r)
			r.p.Node = &hostedNetworkRecord{hostedNoAnnounce: n, record: tc.record, err: tc.err}
			r.reset()
			res, err := r.p.PublishHosted(context.Background(), fixtureID, nil)
			if !tc.wantSuccess {
				if !errors.Is(err, ErrHostedBootstrapNeedsPublish) || res.CID != "" {
					t.Fatalf("uncertain identity was accepted: %+v, %v", res, err)
				}
				if sent, _ := r.counts(); sent != 0 {
					t.Fatalf("uncertain identity uploaded %d bytes", sent)
				}
			} else {
				if err != nil || res.CID == "" || res.Sequence != 1 {
					t.Fatalf("fresh first publication: %+v, %v", res, err)
				}
				actual, err := r.p.InspectSite(context.Background(), r.srv.URL, name)
				if err != nil || actual.CID != res.CID || actual.Sequence != 1 {
					t.Fatalf("fresh publication was not committed and verified: %+v, %v", actual, err)
				}
			}
			hostedAssertNoQueue(t, r.p)
		})
	}
}

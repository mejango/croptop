package mobile

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	boxoipns "github.com/ipfs/boxo/ipns"
	"github.com/ipfs/go-cid"
	libcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/multiformats/go-multihash"
)

type fakePublisher struct {
	mu                                                     sync.Mutex
	site                                                   *store.Site
	head                                                   string
	seq                                                    uint64
	prepared                                               []publish.NewPost
	posts                                                  map[string]publish.Posted
	commits                                                int
	commitErr, inspectErr                                  error
	prepareStarted, prepareWait, commitStarted, commitWait chan struct{}
	commitAccepted, replyWait                              chan struct{}
}

func testCID(value string) string {
	c, _ := cid.Prefix{Version: 1, Codec: cid.Raw, MhType: multihash.SHA2_256, MhLength: -1}.Sum([]byte(value))
	return c.String()
}

func (f *fakePublisher) InspectSite(ctx context.Context, host, name string) (publish.SiteSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inspectErr != nil {
		return publish.SiteSnapshot{}, f.inspectErr
	}
	b, _ := json.Marshal(f.site)
	var site store.Site
	_ = json.Unmarshal(b, &site)
	return publish.SiteSnapshot{Site: &site, CID: f.head, Sequence: f.seq, AcceptsParent: true}, nil
}
func (f *fakePublisher) InspectPost(ctx context.Context, host, name, id string) (*publish.Posted, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inspectErr != nil {
		return nil, f.inspectErr
	}
	post, ok := f.posts[id]
	if !ok {
		return nil, nil
	}
	return &post, nil
}
func (f *fakePublisher) PreparePost(ctx context.Context, host, name string, np publish.NewPost, dir string) (publish.PreparedPost, error) {
	if f.prepareStarted != nil {
		select {
		case f.prepareStarted <- struct{}{}:
		default:
		}
	}
	if f.prepareWait != nil {
		select {
		case <-f.prepareWait:
		case <-ctx.Done():
			return publish.PreparedPost{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.posts[np.ID]; ok {
		return publish.PreparedPost{}, publish.ErrPostExists
	}
	f.prepared = append(f.prepared, np)
	b, _ := json.Marshal(f.site)
	var site store.Site
	_ = json.Unmarshal(b, &site)
	return publish.PreparedPost{Site: &site, Parent: f.head, CID: testCID(np.ID + f.head), Sequence: f.seq + 1, PostID: np.ID, URL: host + "/test/" + np.ID + "/", WorkDir: dir, ChangedDir: filepath.Join(dir, "changed")}, nil
}
func (f *fakePublisher) CommitPreparedPost(ctx context.Context, p publish.PreparedPost, auth publish.PostAuthorization) (publish.Posted, error) {
	if f.commitStarted != nil {
		select {
		case f.commitStarted <- struct{}{}:
		default:
		}
	}
	if f.commitWait != nil {
		select {
		case <-f.commitWait:
		case <-ctx.Done():
			return publish.Posted{}, ctx.Err()
		}
	}
	f.mu.Lock()
	if p.Parent != f.head {
		f.mu.Unlock()
		return publish.Posted{}, publish.ErrParentChanged
	}
	if _, ok := f.posts[p.PostID]; !ok {
		f.commits++
	}
	posted := publish.Posted{Result: publish.Result{CID: p.CID, Sequence: p.Sequence}, URL: p.URL}
	f.posts[p.PostID] = posted
	f.head = p.CID
	f.seq = p.Sequence
	err := f.commitErr
	f.mu.Unlock()
	if f.commitAccepted != nil {
		f.commitAccepted <- struct{}{}
	}
	if f.replyWait != nil {
		select {
		case <-f.replyWait:
		case <-ctx.Done():
			return posted, ctx.Err()
		}
	}
	return posted, err
}

type serviceFixture struct {
	server      *Server
	handler     http.Handler
	publisher   *fakePublisher
	key         ed25519.PrivateKey
	name, token string
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	lpub, _ := libcrypto.UnmarshalEd25519PublicKey(pub)
	pid, _ := peer.IDFromPublicKey(lpub)
	name := boxoipns.NameFromPeer(pid).String()
	site := &store.Site{ID: store.NewID(), IPNS: name, Name: "Phone test"}
	_ = site.SetStorage(store.StorageHosted)
	digest := strings.Repeat("a", 64)
	descriptor, _ := json.Marshal(render.MobileDescriptor{Version: 1, TemplateDigest: digest})
	site.Raw[render.MobileDescriptorKey] = descriptor
	publish.SetHost(site, "https://crop.top")
	fake := &fakePublisher{site: site, head: testCID("initial"), seq: 1, posts: map[string]publish.Posted{}}
	s := &Server{Publisher: fake, DataDir: t.TempDir(), Origin: "https://app.crop.top", HostURL: "https://crop.top", TemplateDigest: digest, Enabled: true}
	f := &serviceFixture{server: s, handler: s.Handler(), publisher: fake, key: key, name: name}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	f.token = f.login(t)
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *serviceFixture) request(t *testing.T, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&b).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, apiPrefix+path, &b)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func requireStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
}
func decodeOperation(t *testing.T, w *httptest.ResponseRecorder) Operation {
	t.Helper()
	var op Operation
	if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil {
		t.Fatal(err)
	}
	return op
}

func (f *serviceFixture) login(t *testing.T) string {
	t.Helper()
	w := f.request(t, "POST", "/challenge", map[string]string{"ipns": f.name}, "")
	requireStatus(t, w, 200)
	var c struct{ ID, Message string }
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	w = f.request(t, "POST", "/session", map[string]any{"id": c.ID, "signature": ed25519.Sign(f.key, []byte(c.Message))}, "")
	requireStatus(t, w, 200)
	var sess struct{ Token string }
	_ = json.Unmarshal(w.Body.Bytes(), &sess)
	return sess.Token
}

func (f *serviceFixture) enable(t *testing.T) {
	t.Helper()
	requireStatus(t, f.request(t, "PUT", "/connection", map[string]bool{"enabled": true}, f.token), 200)
}
func screenshot(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.RGBA{R: 210, G: 220, B: 230, A: 255})
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func (f *serviceFixture) upload(t *testing.T, id, title string) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	m := multipart.NewWriter(&b)
	_ = m.WriteField("id", id)
	_ = m.WriteField("title", title)
	_ = m.WriteField("caption", "Screenshot caption")
	p, _ := m.CreateFormFile("image", "screen.png")
	_, _ = p.Write(screenshot(t))
	_ = m.Close()
	r := httptest.NewRequest("POST", apiPrefix+"/operations", &b)
	r.Header.Set("Content-Type", m.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func (f *serviceFixture) wait(t *testing.T, id, state string) Operation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		f.server.mu.Lock()
		op := f.server.operations[operationKey(f.name, id)]
		var got Operation
		if op != nil {
			got = op.Operation
		}
		active := f.server.active[operationKey(f.name, id)] != nil
		f.server.mu.Unlock()
		if got.State == state && !active {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation state=%s code=%s error=%s, want %s", got.State, got.Code, got.Error, state)
		}
		time.Sleep(time.Millisecond * 5)
	}
}
func (f *serviceFixture) prepare(t *testing.T) Operation {
	t.Helper()
	f.enable(t)
	id := store.NewID()
	w := f.upload(t, id, "Screenshot")
	if w.Code != 202 && w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	return f.wait(t, id, "needs_signature")
}
func (f *serviceFixture) commit(t *testing.T, op Operation) *httptest.ResponseRecorder {
	t.Helper()
	return f.request(t, "POST", "/operations/"+op.ID+"/commit", map[string]any{"proposalId": op.Proposal.ID, "recordSignature": ed25519.Sign(f.key, op.Proposal.RecordPayload), "pushSignature": ed25519.Sign(f.key, op.Proposal.PushPayload)}, f.token)
}

func TestSessionChallengeReplayConsentAndPrivatePersistence(t *testing.T) {
	f := newServiceFixture(t)
	w := f.request(t, "GET", "/site", nil, f.token)
	requireStatus(t, w, 200)
	var site siteResponse
	_ = json.Unmarshal(w.Body.Bytes(), &site)
	if !site.Ready || site.Enabled || site.CID == "" || site.Sequence != "1" {
		t.Fatalf("site readiness: %+v", site)
	}
	requireStatus(t, f.upload(t, store.NewID(), "No consent"), 403)
	authBytes, err := os.ReadFile(filepath.Join(f.server.DataDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(authBytes, []byte(f.token)) {
		t.Fatal("raw bearer token persisted")
	}
	info, _ := os.Stat(filepath.Join(f.server.DataDir, "auth.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatalf("auth mode %v", info.Mode())
	}
	w = f.request(t, "POST", "/challenge", map[string]string{"ipns": f.name}, "")
	var c struct{ ID, Message string }
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	if !strings.HasPrefix(c.Message, "croptop-mobile-session\nhttps://app.crop.top\n"+f.name+"\n"+c.ID+"\n") {
		t.Fatal(c.Message)
	}
	body := map[string]any{"id": c.ID, "signature": ed25519.Sign(f.key, []byte(c.Message))}
	requireStatus(t, f.request(t, "POST", "/session", body, ""), 200)
	requireStatus(t, f.request(t, "POST", "/session", body, ""), 401)
	requireStatus(t, f.request(t, "GET", "/site", nil, "not-a-token"), 401)
	f.server.clock = func() time.Time { return time.Now().Add(13 * time.Hour) }
	requireStatus(t, f.request(t, "GET", "/site", nil, f.token), 401)
}

func TestOperationIdempotencyPreviewAndExactSignatures(t *testing.T) {
	f := newServiceFixture(t)
	op := f.prepare(t)
	if op.PostID != op.ID || op.MediaType != "image/png" || len(op.MediaSHA256) != 64 || op.Proposal.Host != "crop.top" {
		t.Fatalf("bad operation %+v", op)
	}
	w := f.upload(t, op.ID, "Screenshot")
	requireStatus(t, w, 200)
	if decodeOperation(t, w).PostID != op.PostID {
		t.Fatal("post identity changed")
	}
	requireStatus(t, f.upload(t, op.ID, "Different title"), 409)
	requireStatus(t, f.request(t, "GET", "/operations/"+op.ID+"/image", nil, ""), 401)
	w = f.request(t, "GET", "/operations/"+op.ID+"/image", nil, f.token)
	requireStatus(t, w, 200)
	if digest(w.Body.Bytes()) != op.MediaSHA256 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("preview integrity/privacy mismatch")
	}
	wrong := map[string]any{"proposalId": op.Proposal.ID, "recordSignature": ed25519.Sign(f.key, op.Proposal.RecordPayload), "pushSignature": ed25519.Sign(f.key, []byte("other push"))}
	requireStatus(t, f.request(t, "POST", "/operations/"+op.ID+"/commit", wrong, f.token), 401)
	w = f.commit(t, op)
	if w.Code != 202 && w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	published := f.wait(t, op.ID, "published")
	if published.URL == "" {
		t.Fatal("published without link")
	}
	requireStatus(t, f.commit(t, op), 200)
	f.publisher.mu.Lock()
	defer f.publisher.mu.Unlock()
	if f.publisher.commits != 1 || len(f.publisher.prepared) != 1 {
		t.Fatalf("commits %d preparations %d", f.publisher.commits, len(f.publisher.prepared))
	}
	post := f.publisher.prepared[0]
	if post.ID != op.ID || post.Created == nil || post.Created.Unix() != op.CreatedAt || post.HeroImage == "" {
		t.Fatalf("missing retained post metadata %+v", post)
	}
}

func TestLostCommitAfterLaterHeadAndRestartRecoversSamePost(t *testing.T) {
	f := newServiceFixture(t)
	op := f.prepare(t)
	w := f.commit(t, op)
	if w.Code != 202 && w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	f.wait(t, op.ID, "published")
	// Simulate host commit with lost response AND service journal completion.
	// Then another author publishes, so comparing current head is insufficient.
	f.server.mu.Lock()
	retained := f.server.operations[operationKey(f.name, op.ID)]
	retained.State = "committing"
	retained.URL = ""
	if err := f.server.saveOperationLocked(retained); err != nil {
		t.Fatal(err)
	}
	f.server.mu.Unlock()
	f.publisher.mu.Lock()
	f.publisher.head = testCID("later unrelated author")
	f.publisher.seq++
	f.publisher.mu.Unlock()
	restarted := &Server{Publisher: f.publisher, DataDir: f.server.DataDir, Origin: f.server.Origin, HostURL: f.server.HostURL, TemplateDigest: f.server.TemplateDigest, Enabled: true}
	if err := f.server.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restarted.Close(); err != nil {
			t.Error(err)
		}
	})
	f.handler = restarted.Handler()
	f.server = restarted
	w = f.request(t, "GET", "/operations/"+op.ID, nil, f.token)
	requireStatus(t, w, 200)
	recovered := decodeOperation(t, w)
	if recovered.State != "published" || recovered.PostID != op.PostID || recovered.URL == "" {
		t.Fatalf("not recovered %+v", recovered)
	}
	requireStatus(t, f.request(t, "POST", "/operations/"+op.ID+"/prepare", struct{}{}, f.token), 200)
	f.publisher.mu.Lock()
	defer f.publisher.mu.Unlock()
	if f.publisher.commits != 1 || len(f.publisher.prepared) != 1 {
		t.Fatal("retry duplicated original publication")
	}
}

func TestLostCommitReplyReconcilesImmediately(t *testing.T) {
	f := newServiceFixture(t)
	op := f.prepare(t)
	f.publisher.commitErr = errors.New("reply lost")
	w := f.commit(t, op)
	if w.Code != 202 && w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	published := f.wait(t, op.ID, "published")
	if published.URL == "" || published.Error != "" {
		t.Fatalf("lost-reply recovery %+v", published)
	}
}

func TestStaleParentAndExpiryReprepareRetainsIdentity(t *testing.T) {
	f := newServiceFixture(t)
	first := f.prepare(t)
	f.publisher.mu.Lock()
	f.publisher.head = testCID("desktop concurrent post")
	f.publisher.seq++
	f.publisher.mu.Unlock()
	w := f.commit(t, first)
	if w.Code != 202 && w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	failed := f.wait(t, first.ID, "failed")
	if failed.Code != "parent_changed" {
		t.Fatalf("wrong conflict %+v", failed)
	}
	w = f.request(t, "POST", "/operations/"+first.ID+"/prepare", struct{}{}, f.token)
	if w.Code != 202 && w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	next := f.wait(t, first.ID, "needs_signature")
	if next.PostID != first.PostID || next.CreatedAt != first.CreatedAt || next.Proposal.ID == first.Proposal.ID || next.Proposal.Parent == first.Proposal.Parent {
		t.Fatal("rebase changed identity or reused stale proposal")
	}
	f.server.clock = func() time.Time { return time.Now().Add(10 * time.Minute) }
	requireStatus(t, f.commit(t, next), 409)
	f.server.clock = nil
	w = f.commit(t, next)
	if w.Code != 202 && w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	f.wait(t, next.ID, "published")
}

func TestDisableCancelsPreparationAndSerializesCommit(t *testing.T) {
	t.Run("preparing", func(t *testing.T) {
		f := newServiceFixture(t)
		f.enable(t)
		f.publisher.prepareStarted = make(chan struct{}, 1)
		f.publisher.prepareWait = make(chan struct{})
		id := store.NewID()
		_ = f.upload(t, id, "Screenshot")
		select {
		case <-f.publisher.prepareStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("prepare did not start")
		}
		requireStatus(t, f.request(t, "PUT", "/connection", map[string]bool{"enabled": false}, f.token), 200)
		f.wait(t, id, "failed")
		requireStatus(t, f.request(t, "POST", "/operations/"+id+"/prepare", struct{}{}, f.token), 403)
		f.publisher.mu.Lock()
		defer f.publisher.mu.Unlock()
		if f.publisher.commits != 0 {
			t.Fatal("disabled draft committed")
		}
	})
	t.Run("committing", func(t *testing.T) {
		f := newServiceFixture(t)
		op := f.prepare(t)
		f.publisher.commitStarted = make(chan struct{}, 1)
		f.publisher.commitWait = make(chan struct{})
		_ = f.commit(t, op)
		select {
		case <-f.publisher.commitStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("commit did not start")
		}
		stopped := make(chan *httptest.ResponseRecorder, 1)
		go func() { stopped <- f.request(t, "PUT", "/connection", map[string]bool{"enabled": false}, f.token) }()
		select {
		case w := <-stopped:
			requireStatus(t, w, 200)
		case <-time.After(5 * time.Second):
			t.Fatal("stop did not complete")
		}
		f.wait(t, op.ID, "failed")
		requireStatus(t, f.upload(t, store.NewID(), "New post"), 403)
		f.publisher.mu.Lock()
		defer f.publisher.mu.Unlock()
		if f.publisher.commits != 0 {
			t.Fatal("stop allowed an uncommitted post")
		}
	})
	t.Run("already committed", func(t *testing.T) {
		f := newServiceFixture(t)
		op := f.prepare(t)
		f.publisher.commitAccepted = make(chan struct{}, 1)
		f.publisher.replyWait = make(chan struct{})
		_ = f.commit(t, op)
		select {
		case <-f.publisher.commitAccepted:
		case <-time.After(5 * time.Second):
			t.Fatal("host did not commit")
		}
		requireStatus(t, f.request(t, "PUT", "/connection", map[string]bool{"enabled": false}, f.token), 200)
		f.wait(t, op.ID, "published")
	})
}

func TestReadinessOriginAndSiteIsolation(t *testing.T) {
	f := newServiceFixture(t)
	op := f.prepare(t)
	r := httptest.NewRequest("GET", apiPrefix+"/config", nil)
	r.Header.Set("Origin", "https://evil.crop.top")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	requireStatus(t, w, 403)
	f.server.mu.Lock()
	f.server.auth.Sessions[digest([]byte("other"))] = session{IPNS: "other-site", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	f.server.mu.Unlock()
	requireStatus(t, f.request(t, "GET", "/operations/"+op.ID, nil, "other"), 404)
	f.publisher.mu.Lock()
	delete(f.publisher.site.Raw, render.MobileDescriptorKey)
	f.publisher.mu.Unlock()
	w = f.request(t, "GET", "/site", nil, f.token)
	requireStatus(t, w, 200)
	var info siteResponse
	_ = json.Unmarshal(w.Body.Bytes(), &info)
	if info.Ready || info.Reason == "" {
		t.Fatalf("legacy site incorrectly ready %+v", info)
	}
	w = f.commit(t, op)
	if w.Code != 202 && w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	failed := f.wait(t, op.ID, "failed")
	if failed.Code != "site_not_ready" {
		t.Fatalf("compatibility changed before commit %+v", failed)
	}
}

func TestCleanupKeepsReceiptAndNeverExpiresUncertainCommit(t *testing.T) {
	f := newServiceFixture(t)
	op := f.prepare(t)
	f.server.clock = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	f.publisher.mu.Lock()
	f.publisher.inspectErr = errors.New("host unavailable")
	f.publisher.mu.Unlock()
	if err := f.server.Cleanup(context.Background()); err == nil {
		t.Fatal("cleanup ignored uncertainty")
	}
	if _, err := os.Stat(filepath.Join(f.server.DataDir, "operations", operationKey(f.name, op.ID), "upload")); err != nil {
		t.Fatal("uncertain draft removed")
	}
	f.publisher.mu.Lock()
	f.publisher.inspectErr = nil
	f.publisher.mu.Unlock()
	if err := f.server.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.server.DataDir, "operations", operationKey(f.name, op.ID), "upload")); !os.IsNotExist(err) {
		t.Fatal("expired private upload remains")
	}
	if _, err := os.Stat(filepath.Join(f.server.DataDir, "operations", operationKey(f.name, op.ID), "operation.json")); err != nil {
		t.Fatal("recovery identity removed")
	}
}

func TestExclusiveJournalLockAndClose(t *testing.T) {
	f := newServiceFixture(t)
	second := &Server{Publisher: f.publisher, DataDir: f.server.DataDir, Origin: f.server.Origin, HostURL: f.server.HostURL, TemplateDigest: f.server.TemplateDigest, Enabled: true}
	if err := second.Init(); err == nil {
		_ = second.Close()
		t.Fatal("two services accepted the same journal")
	}
	if err := f.server.Close(); err != nil {
		t.Fatal(err)
	}
	third := &Server{Publisher: f.publisher, DataDir: f.server.DataDir, Origin: f.server.Origin, HostURL: f.server.HostURL, TemplateDigest: f.server.TemplateDigest, Enabled: true}
	if err := third.Init(); err != nil {
		t.Fatalf("closed service retained lock: %v", err)
	}
	defer third.Close()
	f.handler = third.Handler()
	requireStatus(t, f.request(t, "GET", "/site", nil, f.token), 200)
}

func TestAuthenticationFloodCannotFillGlobalLimitsForOneSite(t *testing.T) {
	t.Run("challenges", func(t *testing.T) {
		f := newServiceFixture(t)
		for i := 0; i < 20; i++ {
			w := f.request(t, "POST", "/challenge", map[string]string{"ipns": f.name}, "")
			if i < 4 {
				requireStatus(t, w, 200)
			} else {
				requireStatus(t, w, 429)
			}
		}
		other := newServiceFixture(t)
		other.handler = f.handler
		if token := other.login(t); token == "" {
			t.Fatal("another site could not authenticate")
		}
		f.server.mu.Lock()
		defer f.server.mu.Unlock()
		if len(f.server.challenges) > 4 {
			t.Fatal("one site exhausted challenge storage")
		}
	})
	t.Run("sessions", func(t *testing.T) {
		f := newServiceFixture(t)
		for i := 0; i < 20; i++ {
			_ = f.login(t)
		}
		f.server.mu.Lock()
		count := len(f.server.auth.Sessions)
		f.server.mu.Unlock()
		if count != 16 {
			t.Fatalf("session cap: %d", count)
		}
		other := newServiceFixture(t)
		other.handler = f.handler
		if token := other.login(t); token == "" {
			t.Fatal("another site could not authenticate")
		}
	})
	t.Run("socket address", func(t *testing.T) {
		f := newServiceFixture(t)
		for i := 0; i < 125; i++ {
			body, _ := json.Marshal(map[string]string{"ipns": f.name})
			r := httptest.NewRequest("POST", apiPrefix+"/challenge", bytes.NewReader(body))
			r.RemoteAddr = "198.51.100.9:1000"
			r.Header.Set("X-Forwarded-For", testCID(string(rune(i))))
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
		}
		f.server.mu.Lock()
		window := f.server.issuance["198.51.100.9"]
		count := len(f.server.issuance)
		f.server.mu.Unlock()
		if window.Count != 120 || count != 2 {
			t.Fatalf("forwarding headers altered socket buckets: %+v count%d", window, count)
		}
	})
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *deadlineRecorder) SetReadDeadline(t time.Time) error { w.deadline = t; return nil }

func TestMobileBodyReadDeadline(t *testing.T) {
	f := newServiceFixture(t)
	for _, test := range []struct {
		path    string
		seconds int
	}{{"/challenge", 30}, {"/operations", 90}} {
		r := httptest.NewRequest("POST", apiPrefix+test.path, strings.NewReader("{}"))
		w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		f.handler.ServeHTTP(w, r)
		remaining := time.Until(w.deadline)
		if remaining < time.Duration(test.seconds-1)*time.Second || remaining > time.Duration(test.seconds)*time.Second {
			t.Fatalf("%s deadline %v", test.path, remaining)
		}
	}
}

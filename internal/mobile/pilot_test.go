package mobile

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

func restartPilotService(t *testing.T, f *serviceFixture, configure func(*Server)) {
	t.Helper()
	before := f.server
	if err := before.Close(); err != nil {
		t.Fatal(err)
	}
	s := &Server{Publisher: f.publisher, DataDir: before.DataDir, Origin: before.Origin, HostURL: before.HostURL, TemplateDigest: before.TemplateDigest, Enabled: true}
	configure(s)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	f.server, f.handler = s, s.Handler()
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
}

type pilotReadCounter struct{ reads int }

func (r *pilotReadCounter) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }

func TestPilotGlobalDraftLimitAcrossSitesAndRestart(t *testing.T) {
	f := newServiceFixture(t, func(s *Server) { s.MaxOpenOperations = 20 })
	f.enable(t)
	other := newServiceFixture(t)
	f.server.mu.Lock()
	for i := 0; i < 21; i++ {
		id := store.NewID()
		op := &operation{Operation: Operation{ID: id, PostID: id, IPNS: other.name, State: "failed", CreatedAt: f.server.now().Unix()}}
		if i == 19 {
			op.State = "published"
		} else if i == 20 {
			op.Code = "draft_expired"
		}
		if err := f.server.saveOperationLocked(op); err != nil {
			f.server.mu.Unlock()
			t.Fatal(err)
		}
		f.server.operations[operationKey(other.name, id)] = op
	}
	f.server.mu.Unlock()
	id := store.NewID()
	w := f.upload(t, id, "twentieth global draft")
	requireStatus(t, w, 202)
	f.wait(t, id, "needs_signature")
	restartPilotService(t, f, func(s *Server) { s.MaxOpenOperations = 20 })
	body := &pilotReadCounter{}
	r := httptest.NewRequest("POST", apiPrefix+"/operations", body)
	r.Header.Set("Authorization", "Bearer "+f.token)
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	requireStatus(t, w, 429)
	if body.reads != 0 {
		t.Fatal("full service consumed upload bytes before denying admission")
	}
	if !strings.Contains(w.Body.String(), "service_draft_limit") {
		t.Fatal(w.Body.String())
	}
	// Existing operations can still recover and finish when admission is full.
	requireStatus(t, f.request(t, "GET", "/operations/"+id, nil, f.token), 200)
	f.server.mu.Lock()
	defer f.server.mu.Unlock()
	if count := f.server.openOperationCountLocked(); count != 20 {
		t.Fatalf("retained %d open drafts, want exactly 20", count)
	}
	if f.server.pendingUploads != 0 {
		t.Fatal("upload admission reservation leaked")
	}
}

type pilotBlockedReader struct {
	start, release chan struct{}
	reader         io.Reader
	started        bool
}

func (b *pilotBlockedReader) Read(p []byte) (int, error) {
	if !b.started {
		b.started = true
		close(b.start)
		<-b.release
	}
	return b.reader.Read(p)
}

func TestPilotGlobalDraftLimitReservesConcurrentUploads(t *testing.T) {
	f := newServiceFixture(t, func(s *Server) { s.MaxOpenOperations = 1 })
	f.enable(t)
	body := &pilotBlockedReader{start: make(chan struct{}), release: make(chan struct{}), reader: strings.NewReader("--testing--\r\n")}
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("POST", apiPrefix+"/operations", body)
		r.Header.Set("Authorization", "Bearer "+f.token)
		r.Header.Set("Content-Type", "multipart/form-data; boundary=testing")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		finished <- w
	}()
	select {
	case <-body.start:
	case <-time.After(time.Second):
		t.Fatal("first upload did not begin")
	}
	w := f.upload(t, store.NewID(), "concurrent draft")
	requireStatus(t, w, 429)
	close(body.release)
	requireStatus(t, <-finished, 400)
	// Invalid/aborted uploads release their reservation for the next attempt.
	requireStatus(t, f.upload(t, store.NewID(), "replacement draft"), 202)
}

func TestPilotAllowlistValidatesAndDoesNotExposeIdentities(t *testing.T) {
	for _, name := range []string{"", "not-an-ipns-name", "k51invalid", "/ipns/k51invalid"} {
		s := &Server{Publisher: &fakePublisher{}, DataDir: t.TempDir(), Origin: "https://composer.example", HostURL: "https://crop.top", TemplateDigest: strings.Repeat("a", 64), AllowSites: []string{name}}
		if err := s.Init(); err == nil {
			_ = s.Close()
			t.Fatalf("invalid allowlist entry %q accepted", name)
		}
	}
	f := newServiceFixture(t, func(s *Server) {
		s.AllowSites = []string{s.Publisher.(*fakePublisher).site.IPNS}
	})
	requireStatus(t, f.request(t, "GET", "/site", nil, f.token), 200)
	requireStatus(t, f.request(t, "PUT", "/connection", map[string]bool{"enabled": true}, f.token), 200)
	w := f.request(t, "GET", "/config", nil, "")
	requireStatus(t, w, 200)
	if strings.Contains(w.Body.String(), f.name) || strings.Contains(w.Body.String(), "allowSites") {
		t.Fatal("config disclosed the operator allowlist")
	}
	// The initialized policy is copied, not aliased to the caller's slice.
	f.server.AllowSites[0] = "changed"
	if !f.server.siteAllowed(f.name) {
		t.Fatal("initialized allowlist changed through configuration slice")
	}
}

func TestPilotAllowlistRejectsBeforeFetchingPublishedState(t *testing.T) {
	other := newServiceFixture(t)
	f := newServiceFixture(t, func(s *Server) {
		s.AllowSites = []string{s.Publisher.(*fakePublisher).site.IPNS}
	})
	w := f.request(t, "POST", "/challenge", map[string]string{"ipns": other.name}, "")
	requireStatus(t, w, 403)
	if !strings.Contains(w.Body.String(), "site_not_allowed") {
		t.Fatal(w.Body.String())
	}
	if _, err := f.server.inspect(context.Background(), other.name); err == nil {
		t.Fatal("unlisted internal inspection accepted")
	}
	// Defend session creation independently of challenge creation as well.
	f.server.mu.Lock()
	f.server.challenges["unlisted"] = challenge{IPNS: other.name, Message: "test challenge", ExpiresAt: f.server.now().Add(challengeLifetime).Unix()}
	f.server.auth.Connections[other.name] = true
	allowed := f.server.allowedLocked(other.name)
	f.server.mu.Unlock()
	if allowed {
		t.Fatal("background work allowed for an unlisted connected site")
	}
	w = f.request(t, "POST", "/session", map[string]any{"id": "unlisted", "signature": ed25519.Sign(other.key, []byte("test challenge"))}, "")
	requireStatus(t, w, 403)
	f.publisher.mu.Lock()
	defer f.publisher.mu.Unlock()
	if f.publisher.inspections != 0 {
		t.Fatalf("denied requests fetched published state %d times", f.publisher.inspections)
	}
}

func TestPilotAllowlistRemovalRejectsPersistedSessions(t *testing.T) {
	f := newServiceFixture(t)
	f.enable(t)
	other := newServiceFixture(t)
	restartPilotService(t, f, func(s *Server) { s.AllowSites = []string{other.name} })
	f.publisher.mu.Lock()
	before := f.publisher.inspections
	f.publisher.mu.Unlock()
	for _, path := range []string{"/site", "/operations/saved", "/operations/saved/image"} {
		requireStatus(t, f.request(t, "GET", path, nil, f.token), 401)
	}
	requireStatus(t, f.request(t, "PUT", "/connection", map[string]bool{"enabled": true}, f.token), 401)
	requireStatus(t, f.request(t, "POST", "/challenge", map[string]string{"ipns": f.name}, ""), 403)
	f.publisher.mu.Lock()
	defer f.publisher.mu.Unlock()
	if f.publisher.inspections != before {
		t.Fatal("removed site's persisted credential fetched published state")
	}
}

func TestPilotTrustedProxyProtectsEveryNonHealthRoute(t *testing.T) {
	f := newServiceFixture(t)
	// Configuration is normally fixed before Handler; restart so credentials
	// created before protection was enabled remain valid behind the edge only.
	restartPilotService(t, f, func(s *Server) { s.ProxySecret = "pilot-edge-test-secret" })
	s := f.server
	request := func(method, path, secret string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, apiPrefix+path, nil)
		r.Header.Set("Authorization", "Bearer "+f.token)
		r.Header.Set("X-Croptop-Mobile-Proxy", secret)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	for _, methodPath := range [][2]string{{"GET", "/config"}, {"GET", "/site"}, {"POST", "/challenge"}, {"POST", "/session"}, {"POST", "/pairings/123/claim"}, {"POST", "/operations"}, {"OPTIONS", "/config"}, {"POST", "/health"}} {
		for _, secret := range []string{"", "wrong", s.ProxySecret + "wrong"} {
			requireStatus(t, request(methodPath[0], methodPath[1], secret), http.StatusForbidden)
		}
	}
	requireStatus(t, request("GET", "/config", s.ProxySecret), 200)
	requireStatus(t, request("GET", "/site", s.ProxySecret), 200)
	for _, method := range []string{"GET", "HEAD"} {
		w := request(method, "/health", "")
		requireStatus(t, w, 200)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("health must not be cached")
		}
		if method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("health HEAD returned a body")
		}
		if method == "GET" {
			var health map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil || len(health) != 1 || health["status"] != "ready" {
				t.Fatalf("health exposed unexpected state: %s", w.Body.String())
			}
		}
	}
}

func TestHostedEnrollmentNeedsProofAndCompatibleSiteButNeverEnablesConsent(t *testing.T) {
	f := newServiceFixture(t, func(s *Server) { s.RequireHostedSite = true })
	f.server.mu.Lock()
	if f.server.auth.Connections[f.name] {
		t.Fatal("successful enrollment silently enabled phone publishing")
	}
	sessionsBefore := len(f.server.auth.Sessions)
	f.server.mu.Unlock()
	f.publisher.mu.Lock()
	inspectionsBefore := f.publisher.inspections
	f.publisher.mu.Unlock()
	challenge := func() (string, string) {
		w := f.request(t, "POST", "/challenge", map[string]string{"ipns": f.name}, "")
		requireStatus(t, w, 200)
		var c struct{ ID, Message string }
		if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		return c.ID, c.Message
	}
	id, _ := challenge()
	requireStatus(t, f.request(t, "POST", "/session", map[string]any{"id": id, "signature": []byte("invalid")}, ""), 401)
	f.publisher.mu.Lock()
	if f.publisher.inspections != inspectionsBefore {
		t.Fatal("invalid signature triggered upstream reads")
	}
	delete(f.publisher.site.Raw, render.MobileDescriptorKey)
	f.publisher.mu.Unlock()
	id, message := challenge()
	w := f.request(t, "POST", "/session", map[string]any{"id": id, "signature": ed25519.Sign(f.key, []byte(message))}, "")
	requireStatus(t, w, 409)
	if !strings.Contains(w.Body.String(), "site_not_ready") {
		t.Fatal(w.Body.String())
	}
	f.server.mu.Lock()
	defer f.server.mu.Unlock()
	if len(f.server.auth.Sessions) != sessionsBefore {
		t.Fatal("legacy site allocated a durable enrollment session")
	}
	if _, exists := f.server.challenges[id]; exists {
		t.Fatal("failed enrollment retained a replayable challenge")
	}
}

func TestHostedEnrollmentDoesNotHoldAuthLockDuringNetwork(t *testing.T) {
	f := newServiceFixture(t, func(s *Server) { s.RequireHostedSite = true })
	f.publisher.inspectStarted = make(chan struct{}, 1)
	f.publisher.inspectWait = make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(f.publisher.inspectWait)
		}
	}()
	w := f.request(t, "POST", "/challenge", map[string]string{"ipns": f.name}, "")
	requireStatus(t, w, 200)
	var c struct{ ID, Message string }
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	sessionResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		sessionResult <- f.request(t, "POST", "/session", map[string]any{"id": c.ID, "signature": ed25519.Sign(f.key, []byte(c.Message))}, "")
	}()
	select {
	case <-f.publisher.inspectStarted:
	case <-time.After(time.Second):
		t.Fatal("enrollment never reached upstream inspection")
	}
	concurrentResult := make(chan *httptest.ResponseRecorder, 1)
	go func() { concurrentResult <- f.request(t, "GET", "/health", nil, "") }()
	select {
	case w := <-concurrentResult:
		requireStatus(t, w, 200)
	case <-time.After(time.Second):
		t.Fatal("upstream inspection held auth mutex and blocked readiness")
	}
	close(f.publisher.inspectWait)
	released = true
	requireStatus(t, <-sessionResult, 200)
}

func TestMobileHealthReadinessTracksInitializationAndClose(t *testing.T) {
	f := newServiceFixture(t)
	requireStatus(t, f.request(t, "GET", "/health", nil, ""), 200)
	if err := f.server.Close(); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, f.request(t, "GET", "/health", nil, ""), 503)
	for _, s := range []*Server{
		{},
		{Publisher: f.publisher, DataDir: t.TempDir(), Origin: f.server.Origin, HostURL: f.server.HostURL, TemplateDigest: f.server.TemplateDigest, Enabled: false},
	} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", apiPrefix+"/health", nil))
		requireStatus(t, w, 503)
		_ = s.Close()
	}
}

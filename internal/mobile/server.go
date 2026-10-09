// Package mobile serves the keyless phone publishing API. Device signatures
// authorize every publication; the service holds only private draft staging.
package mobile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
)

const (
	apiPrefix         = "/v0/mobile"
	jobTimeout        = 3 * time.Minute
	sessionLifetime   = 12 * time.Hour
	challengeLifetime = 5 * time.Minute
	draftLifetime     = 7 * 24 * time.Hour
	proposalLifetime  = 9 * time.Minute
	MaxTitleBytes     = 1000
	MaxCaptionBytes   = 10000
)

// Publisher is the keyless boundary shared with the command-line publisher.
type Publisher interface {
	PreparePost(context.Context, string, string, publish.NewPost, string) (publish.PreparedPost, error)
	CommitPreparedPost(context.Context, publish.PreparedPost, publish.PostAuthorization) (publish.Posted, error)
	InspectSite(context.Context, string, string) (publish.SiteSnapshot, error)
	InspectPost(context.Context, string, string, string) (*publish.Posted, error)
}

// Server configuration must be set before Handler is called and must not change
// while it serves requests. Origin and HostURL are operator-selected allowlists,
// never request parameters. The service locks DataDir until Close returns.
type Server struct {
	Publisher                                Publisher
	DataDir, Origin, HostURL, TemplateDigest string
	Enabled                                  bool
	Pairing                                  *PairingRelay
	// AllowSites limits the pilot to these Ed25519 IPNS names. Empty permits
	// any valid site identity; the list is never returned to clients.
	AllowSites []string
	// ProxySecret optionally requires the trusted edge's shared credential.
	// Only readiness GET/HEAD bypasses it. Never expose it in config or logs.
	ProxySecret string
	// RequireHostedSite enrolls only proven owners of a compatible published
	// site. It is useful for a public pilot without manual identity registration.
	RequireHostedSite bool
	// MaxOpenOperations bounds retained drafts across all sites. Zero keeps
	// the historical per-site limit only. Admission is reserved before upload.
	MaxOpenOperations int

	once           sync.Once
	initErr        error
	mu             sync.Mutex
	auth           authState
	challenges     map[string]challenge
	issuance       map[string]issuanceWindow
	operations     map[string]*operation
	active         map[string]*job
	gates          map[string]*sync.Mutex
	slots          chan struct{}
	uploads        chan struct{}
	normalizers    chan struct{}
	enrollments    chan struct{}
	clock          func() time.Time
	closed         bool
	shutdown       context.CancelFunc
	wg             sync.WaitGroup
	dataLock       io.Closer
	allowedSites   map[string]struct{}
	pendingUploads int
}

type job struct{ cancel context.CancelFunc }

func (s *Server) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

func validOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()))
}

func (s *Server) initialize() {
	if !validOrigin(s.Origin) || !validOrigin(s.HostURL) || s.Origin != strings.TrimRight(s.Origin, "/") || s.HostURL != strings.TrimRight(s.HostURL, "/") {
		s.initErr = errors.New("mobile origin and publishing host must be fixed HTTPS origins (loopback HTTP is allowed for testing)")
		return
	}
	if s.DataDir == "" || s.Publisher == nil || s.TemplateDigest == "" {
		s.initErr = errors.New("mobile publisher, private data directory, and template digest are required")
		return
	}
	if s.MaxOpenOperations < 0 {
		s.initErr = errors.New("mobile open-operation limit must not be negative")
		return
	}
	if len(s.AllowSites) != 0 {
		s.allowedSites = make(map[string]struct{}, len(s.AllowSites))
		for _, name := range s.AllowSites {
			if !validSiteIdentity(name) {
				s.initErr = errors.New("mobile allowlist contains an invalid Ed25519 IPNS name")
				return
			}
			s.allowedSites[name] = struct{}{}
		}
	}
	s.challenges = make(map[string]challenge)
	s.issuance = make(map[string]issuanceWindow)
	s.operations = make(map[string]*operation)
	s.active = make(map[string]*job)
	s.gates = make(map[string]*sync.Mutex)
	s.slots = make(chan struct{}, 4)
	s.uploads = make(chan struct{}, 4)
	s.normalizers = make(chan struct{}, 1)
	s.enrollments = make(chan struct{}, 4)
	if s.Pairing == nil {
		s.Pairing = NewPairingRelay(s.Origin)
	}
	s.auth = authState{Sessions: make(map[string]session), Connections: make(map[string]bool)}
	if err := os.MkdirAll(s.DataDir, 0700); err != nil {
		s.initErr = err
		return
	}
	if err := os.Chmod(s.DataDir, 0700); err != nil {
		s.initErr = err
		return
	}
	var lockErr error
	s.dataLock, lockErr = lockDirectory(s.DataDir)
	if lockErr != nil {
		s.initErr = lockErr
		return
	}
	defer func() {
		if s.initErr != nil {
			_ = s.dataLock.Close()
			s.dataLock = nil
		}
	}()
	if staged, ok := s.Publisher.(interface{ CleanupStaging(string) error }); ok {
		if err := staged.CleanupStaging(filepath.Join(s.DataDir, "private-nodes")); err != nil {
			s.initErr = err
			return
		}
	}
	if b, err := os.ReadFile(filepath.Join(s.DataDir, "auth.json")); err == nil {
		if err := json.Unmarshal(b, &s.auth); err != nil {
			s.initErr = fmt.Errorf("mobile authentication journal: %w", err)
			return
		}
	} else if !os.IsNotExist(err) {
		s.initErr = err
		return
	}
	if s.auth.Sessions == nil {
		s.auth.Sessions = make(map[string]session)
	}
	if s.auth.Connections == nil {
		s.auth.Connections = make(map[string]bool)
	}
	s.initErr = s.loadOperations()
	if s.initErr == nil {
		ctx, cancel := context.WithCancel(context.Background())
		s.shutdown = cancel
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					cleanupCtx, stop := context.WithTimeout(ctx, jobTimeout)
					_ = s.Cleanup(cleanupCtx)
					stop()
				}
			}
		}()
	}
}

// Handler serves only /v0/mobile paths. Drafts are never exposed by FileServer.
func (s *Server) Init() error { s.once.Do(s.initialize); return s.initErr }

func (s *Server) Handler() http.Handler {
	_ = s.Init()
	return http.HandlerFunc(s.serveHTTP)
}

// Close stops accepting new work, cancels background work, and waits for active
// operations to leave durable recovery state. Confirmed host commits survive.
func (s *Server) Close() error {
	if err := s.Init(); err != nil {
		return err
	}
	s.mu.Lock()
	s.closed = true
	if s.shutdown != nil {
		s.shutdown()
	}
	for _, active := range s.active {
		active.cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.dataLock != nil {
			err := s.dataLock.Close()
			s.dataLock = nil
			return err
		}
		return nil
	case <-time.After(jobTimeout + time.Second):
		return errors.New("phone publisher jobs did not stop before the shutdown deadline")
	}
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	// Bound slow request bodies as well as their byte count. These deadlines
	// are scoped to this HTTP request, leaving the host's larger upload API alone.
	readWindow := 30 * time.Second
	if r.Method == "POST" && r.URL.Path == apiPrefix+"/operations" {
		readWindow = 90 * time.Second
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(readWindow))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if s.initErr != nil {
		apiError(w, 503, "unavailable", "Phone publishing is not configured correctly.")
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		apiError(w, 503, "unavailable", "Phone publishing is shutting down. Keep your draft and retry shortly.")
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	// Health is deliberately cheap and reveals neither identities, storage
	// paths nor upstream details. It proves initialization, not host reachability.
	if r.URL.Path == apiPrefix+"/health" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		if !s.Enabled {
			apiError(w, 503, "unavailable", "Phone publishing is unavailable.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "{\"status\":\"ready\"}\n")
		}
		return
	}
	if !s.trustedProxy(r) {
		apiError(w, http.StatusForbidden, "proxy_required", "Use the configured Croptop composer.")
		return
	}
	// Native apps omit Origin; browser requests must originate at the dedicated
	// trusted composer. Never reflect arbitrary origins or allow credentials.
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.Origin {
		apiError(w, 403, "origin", "Use the connected Croptop app origin.")
		return
	}
	if r.Header.Get("Origin") == s.Origin {
		w.Header().Set("Access-Control-Allow-Origin", s.Origin)
		w.Header().Set("Vary", "Origin")
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Croptop-Pairing")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, apiPrefix)
	if path == r.URL.Path {
		apiError(w, 404, "not_found", "Not found.")
		return
	}
	if path == "/config" && r.Method == "GET" {
		host, _ := url.Parse(s.HostURL)
		writeJSON(w, 200, map[string]any{"version": 1, "enabled": s.Enabled, "origin": s.Origin, "host": host.Hostname(), "maxImageBytes": MaxImageBytes, "maxImagePixels": MaxImagePixels, "formats": SupportedFormats(), "maxImages": 1, "maxTitleBytes": MaxTitleBytes, "maxCaptionBytes": MaxCaptionBytes})
		return
	}
	if path == "/challenge" && r.Method == "POST" {
		s.challengeHTTP(w, r)
		return
	}
	if path == "/session" && r.Method == "POST" {
		s.sessionHTTP(w, r)
		return
	}
	ipns := s.authenticate(r)
	if path == "/pairings" || strings.HasPrefix(path, "/pairings/") {
		if path == "/pairings" && r.Method == "POST" && ipns != "" {
			s.mu.Lock()
			enabled := s.allowedLocked(ipns)
			s.mu.Unlock()
			if !enabled {
				apiError(w, 403, "connection_disabled", "Enable phone publishing before connecting another device.")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			if _, err := s.inspect(ctx, ipns); err != nil {
				apiError(w, 409, "site_not_ready", err.Error())
				return
			}
		}
		s.Pairing.ServeHTTP(w, r, ipns)
		return
	}
	if ipns == "" {
		apiError(w, 401, "session_expired", "Reconnect this device to continue. Your draft is still available.")
		return
	}
	if path == "/site" && r.Method == "GET" {
		s.siteHTTP(w, r, ipns)
		return
	}
	if path == "/connection" && r.Method == "PUT" {
		s.connectionHTTP(w, r, ipns)
		return
	}
	if path == "/operations" && r.Method == "POST" {
		s.createHTTP(w, r, ipns)
		return
	}
	if strings.HasPrefix(path, "/operations/") {
		s.operationHTTP(w, r, ipns, strings.TrimPrefix(path, "/operations/"))
		return
	}
	apiError(w, 404, "not_found", "Not found.")
}

type siteResponse struct {
	IPNS     string `json:"ipns"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Ready    bool   `json:"ready"`
	Enabled  bool   `json:"enabled"`
	Reason   string `json:"reason,omitempty"`
	CID      string `json:"cid"`
	Sequence string `json:"sequence"`
}

func (s *Server) inspect(ctx context.Context, name string) (publish.SiteSnapshot, error) {
	if !s.siteAllowed(name) {
		return publish.SiteSnapshot{}, errors.New("This site is not included in the phone publishing pilot.")
	}
	head, err := s.Publisher.InspectSite(ctx, s.HostURL, name)
	if err != nil {
		return head, err
	}
	if head.Site == nil || head.Site.IPNS != name {
		return head, errors.New("The published site identity does not match this device.")
	}
	if !head.Site.HostingEnabled() {
		return head, errors.New("Enable hosted storage and publish from the existing publisher first.")
	}
	if !head.AcceptsParent {
		return head, errors.New("The hosting service needs an update before it can add phone posts safely.")
	}
	if publish.HostOf(head.Site) != s.HostURL {
		return head, errors.New("This site's published hosting destination differs from this phone publisher.")
	}
	if err := render.CheckMobileCompatibility(head.Site, s.TemplateDigest); err != nil {
		return head, err
	}
	return head, nil
}

func (s *Server) siteInfo(ctx context.Context, name string) siteResponse {
	head, err := s.inspect(ctx, name)
	res := siteResponse{IPNS: name, URL: s.HostURL + "/ipns/" + name + "/", CID: head.CID, Sequence: strconv.FormatUint(head.Sequence, 10)}
	if head.Site != nil {
		res.Name = head.Site.Name
	}
	s.mu.Lock()
	res.Enabled = s.auth.Connections[name]
	s.mu.Unlock()
	res.Ready = err == nil && s.Enabled
	if err != nil {
		res.Reason = err.Error()
	} else if !s.Enabled {
		res.Reason = "Phone publishing is temporarily unavailable. Existing operation status remains available."
	}
	return res
}

func (s *Server) siteHTTP(w http.ResponseWriter, r *http.Request, name string) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, 200, s.siteInfo(ctx, name))
}

func (s *Server) siteGate(name string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gates[name] == nil {
		s.gates[name] = &sync.Mutex{}
	}
	return s.gates[name]
}

func (s *Server) connectionHTTP(w http.ResponseWriter, r *http.Request, name string) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		apiError(w, 400, "invalid_request", "Choose whether to enable phone publishing.")
		return
	}
	if *body.Enabled && !s.Enabled {
		apiError(w, 503, "unavailable", "Phone publishing is temporarily unavailable.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if *body.Enabled {
		if _, err := s.inspect(ctx, name); err != nil {
			apiError(w, 409, "site_not_ready", err.Error())
			return
		}
	}
	if !*body.Enabled {
		// Stop work immediately, then take the commit gate before acknowledging
		// suspension. A host that already committed is reconciled as published.
		s.mu.Lock()
		for key, op := range s.operations {
			if op.IPNS == name {
				if active := s.active[key]; active != nil {
					active.cancel()
				}
			}
		}
		s.mu.Unlock()
	}
	gate := s.siteGate(name)
	gate.Lock()
	s.mu.Lock()
	before := s.auth.Connections[name]
	s.auth.Connections[name] = *body.Enabled
	err := s.saveAuthLocked()
	if err != nil {
		s.auth.Connections[name] = before
	}
	if err == nil && !*body.Enabled {
		for key, op := range s.operations {
			if op.IPNS == name {
				if active := s.active[key]; active != nil {
					active.cancel()
				}
			}
		}
	}
	s.mu.Unlock()
	gate.Unlock()
	if err != nil {
		apiError(w, 500, "storage", "Could not save the connection. Try again.")
		return
	}
	writeJSON(w, 200, s.siteInfo(ctx, name))
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		apiError(w, 400, "invalid_request", "Invalid request JSON.")
		return false
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		apiError(w, 400, "invalid_request", "One JSON object is required.")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func apiError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": message, "code": code})
}
func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

package server

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mejango/croptop/internal/mobile"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	qrcode "github.com/skip2/go-qrcode"
)

// The ordinary desktop build connects to the deployed, dedicated key-holding
// origin. Isolated deployments may still override it at build time without
// requiring GUI users to set shell flags.
// -ldflags '-X github.com/mejango/croptop/internal/server.defaultMobileOrigin=https://…'
var defaultMobileOrigin = "https://croptop-phone-923c1bafd14ea328.croptop.workers.dev"

type phoneConnection struct {
	SiteID, Name, Token, URL string
	Info                     mobile.PairingInfo
	Private                  *ecdh.PrivateKey
	Context                  context.Context
	Cancel                   context.CancelFunc
}

type phoneConnections struct {
	mu           sync.Mutex
	entries      map[string]*phoneConnection
	preparations map[string]*phonePreparation
}

func (s *Server) phoneConnections() *phoneConnections {
	s.phoneOnce.Do(func() {
		s.phone = &phoneConnections{entries: make(map[string]*phoneConnection), preparations: make(map[string]*phonePreparation)}
	})
	return s.phone
}

func mobilePhoneAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !consoleRequestAllowed(r, "X-Croptop-Phone", "/", "/mobile-connect", "/mobile-connect.html") {
		writeErr(w, 403, errors.New("open Connect phone from the Croptop publisher"))
		return false
	}
	if !shopSecureContext(r) {
		writeErr(w, 400, errors.New("open Connect phone on localhost or HTTPS"))
		return false
	}
	if r.Method == http.MethodPost {
		kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || kind != "application/json" {
			writeErr(w, 415, errors.New("phone connection requests require JSON"))
			return false
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	return true
}

func (s *Server) routesMobilePhone(mux *http.ServeMux) {
	mux.HandleFunc("GET /mobile-connect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'self'; img-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		http.ServeFileFS(w, r, s.UI, "mobile-connect.html")
	})
	for _, name := range []string{"mobile-connect.js", "mobile-connect.css"} {
		mux.HandleFunc("GET /"+name, func(w http.ResponseWriter, r *http.Request) { http.ServeFileFS(w, r, s.UI, name) })
	}
	mux.HandleFunc("POST /v0/croptop/sites/{id}/phone", s.openPhoneConnection)
	mux.HandleFunc("POST /v0/croptop/sites/{id}/phone/preparations", s.startPhonePreparation)
	mux.HandleFunc("GET /v0/croptop/sites/{id}/phone/preparations/{prepid}", s.phonePreparationStatus)
	mux.HandleFunc("DELETE /v0/croptop/sites/{id}/phone/preparations/{prepid}", s.cancelPhonePreparation)
	mux.HandleFunc("GET /v0/croptop/sites/{id}/phone/{pairid}", s.phoneConnectionStatus)
	mux.HandleFunc("DELETE /v0/croptop/sites/{id}/phone/{pairid}", s.cancelPhoneConnection)
	// A generic final segment lets the more-specific preparations route own
	// its namespace without an ambiguous /preparations/qr ServeMux overlap.
	mux.HandleFunc("GET /v0/croptop/sites/{id}/phone/{pairid}/{action}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("action") != "qr" {
			http.NotFound(w, r)
			return
		}
		s.phoneConnectionQR(w, r)
	})
	mux.HandleFunc("POST /v0/croptop/sites/{id}/phone/{pairid}/confirm", s.confirmPhoneConnection)
}

func (s *Server) mobileOrigin() (string, error) {
	origin := s.MobileOrigin
	if origin == "" {
		origin = defaultMobileOrigin
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return "", errors.New("invalid configured phone service origin")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()))) {
		return "", errors.New("phone service requires HTTPS")
	}
	return origin, nil
}

func (s *Server) phoneAPI(ctx context.Context, method, path, token string, in, out any) error {
	origin, err := s.mobileOrigin()
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+"/v0/mobile"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := http.Client{Timeout: 45 * time.Second}
	if s.MobileHTTP != nil {
		client = *s.MobileHTTP
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("phone service could not be reached: %w", err)
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, 32<<10)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = json.NewDecoder(reader).Decode(&problem)
		if problem.Error == "" {
			problem.Error = "phone service could not complete this request"
		}
		return &phoneServiceError{Status: response.StatusCode, Code: problem.Code, Message: problem.Error}
	}
	if out == nil {
		_, err = io.Copy(io.Discard, reader)
		return err
	}
	return json.NewDecoder(reader).Decode(out)
}

type phoneServiceError struct {
	Status        int
	Code, Message string
}

func (e *phoneServiceError) Error() string { return e.Message }

func (s *Server) phoneSession(ctx context.Context, site *store.Site) (string, error) {
	origin, err := s.mobileOrigin()
	if err != nil {
		return "", err
	}
	var challenge struct {
		ID, Message string
		ExpiresAt   int64
	}
	if err := s.phoneAPI(ctx, "POST", "/challenge", "", map[string]string{"ipns": site.IPNS}, &challenge); err != nil {
		return "", err
	}
	expected := fmt.Sprintf("croptop-mobile-session\n%s\n%s\n%s\n%d", origin, site.IPNS, challenge.ID, challenge.ExpiresAt)
	now := time.Now().Unix()
	if !phoneOpaqueToken(challenge.ID) || challenge.Message != expected || challenge.ExpiresAt <= now || challenge.ExpiresAt > now+600 {
		return "", errors.New("phone service returned an invalid connection challenge")
	}
	if _, err := s.currentPhoneSite(ctx, site, site.HostingEnabled()); err != nil {
		return "", err
	}
	signature, err := s.Node.Keystore().Sign(site.ID, []byte(expected))
	if err != nil {
		return "", err
	}
	var session struct {
		Token string `json:"token"`
	}
	if err := s.phoneAPI(ctx, "POST", "/session", "", map[string]string{"id": challenge.ID, "signature": base64.StdEncoding.EncodeToString(signature)}, &session); err != nil {
		return "", err
	}
	if !phoneOpaqueToken(session.Token) {
		return "", errors.New("invalid phone service session")
	}
	return session.Token, nil
}

func (s *Server) openPhoneConnection(w http.ResponseWriter, r *http.Request) {
	if !mobilePhoneAllowed(w, r) {
		return
	}
	var in struct {
		EnableHosting bool `json:"enableHosting"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, errors.New("invalid connection request"))
		return
	}
	site, ok := s.site(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	connection, err := s.preparePhone(ctx, site.ID, phonePreparationOptions{EnableHosting: in.EnableHosting, AllowPublish: true, Legacy: true}, func(string) {})
	if err != nil {
		writePhoneProblem(w, err)
		return
	}
	writeJSON(w, 201, connection)
}

type phoneProblem struct {
	Status int
	Code   string
	Err    error
}

func (e *phoneProblem) Error() string { return e.Err.Error() }
func (e *phoneProblem) Unwrap() error { return e.Err }

func phoneFailure(status int, err error) *phoneProblem {
	problem := &phoneProblem{Status: status, Err: err}
	var local *phoneProblem
	if errors.As(err, &local) {
		problem.Status, problem.Code = local.Status, local.Code
		return problem
	}
	var service *phoneServiceError
	if errors.As(err, &service) {
		problem.Code = service.Code
		if service.Status == 429 {
			problem.Status = 429
		}
	}
	return problem
}

func writePhoneProblem(w http.ResponseWriter, err error) {
	var problem *phoneProblem
	if errors.As(err, &problem) {
		body := map[string]string{"error": problem.Error()}
		if problem.Code != "" {
			body["code"] = problem.Code
		}
		writeJSON(w, problem.Status, body)
		return
	}
	writeErr(w, 500, err)
}

type phoneConnectionResult struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expiresAt"`
	State     string `json:"state"`
	IPNS      string `json:"ipns"`
	Name      string `json:"name"`
}

type phonePreparationOptions struct{ EnableHosting, AllowPublish, Legacy bool }

// preparePhone is the single owner for native, browser and legacy transports.
// Only a required publication holds the render gate. Existing eligible hosted
// content connects without publishing this computer's pending changes.
func (s *Server) preparePhone(ctx context.Context, siteID string, options phonePreparationOptions, progress func(string)) (*phoneConnectionResult, error) {
	site, err := s.Store.Site(siteID)
	if err != nil {
		return nil, phoneFailure(404, errors.New("site not found"))
	}
	if s.Node == nil || s.Pub == nil || !s.Node.Keystore().Has(site.ID) {
		return nil, phoneFailure(409, errors.New("connect from the publisher that has this site's key"))
	}
	if publish.HostOf(site) != publish.DefaultHost {
		return nil, &phoneProblem{Status: 409, Code: "unsupported_host", Err: errors.New("phone posting currently supports sites hosted on crop.top")}
	}
	if site.IsArchived() {
		return nil, phoneFailure(409, errors.New("restore this archived site before connecting a phone"))
	}
	if !site.HostingEnabled() && !options.EnableHosting {
		return nil, &phoneProblem{Status: 409, Code: "hosting_required", Err: errors.New("Allow crop.top to host this site so your phone can publish while this computer sleeps.")}
	}
	progress("service")
	var config struct {
		Enabled bool   `json:"enabled"`
		Origin  string `json:"origin"`
		Version int    `json:"version"`
	}
	if err := s.phoneAPI(ctx, "GET", "/config", "", nil, &config); err != nil {
		return nil, phoneFailure(502, err)
	}
	origin, _ := s.mobileOrigin()
	if !config.Enabled || config.Version != 1 || config.Origin != origin {
		return nil, phoneFailure(503, errors.New("phone posting is not enabled at this service yet"))
	}
	progress("checking")
	var token string
	needsPublication := options.Legacy || !site.HostingEnabled()
	if !needsPublication {
		token, err = s.phoneSession(ctx, site)
		if err == nil {
			var ready phoneReadiness
			err = s.phoneAPI(ctx, "GET", "/site", token, nil, &ready)
			if err == nil {
				if ready.IPNS != site.IPNS || ready.CID == "" {
					return nil, phoneFailure(502, errors.New("phone service returned a different or unidentified publication"))
				}
				if _, seqErr := strconv.ParseUint(ready.Sequence, 10, 64); seqErr != nil {
					return nil, phoneFailure(502, errors.New("phone service returned an invalid publication sequence"))
				}
				if !ready.Ready {
					err = &phoneServiceError{Status: 409, Code: "site_not_ready", Message: ready.Reason}
				}
			}
		}
		if err != nil {
			var service *phoneServiceError
			if !errors.As(err, &service) || service.Code != "site_not_ready" {
				return nil, phoneFailure(502, err)
			}
			// Older services wrap upstream failures in site_not_ready too. Verify
			// the published head independently before classifying a bootstrap;
			// a timeout or unavailable host never authorizes publishing edits.
			needsPublication, err = s.phoneNeedsPublication(ctx, site)
			if err != nil {
				return nil, phoneFailure(502, err)
			}
			if !needsPublication {
				return nil, phoneFailure(409, service)
			}
		}
	}
	if needsPublication {
		if !options.AllowPublish {
			return nil, &phoneProblem{Status: 409, Code: "publication_required", Err: errors.New("This published site needs a hosting or compatibility update. Allow publishing this computer's current site, including saved changes, to continue.")}
		}
		progress("waiting")
		if err := s.lockPhoneContext(ctx); err != nil {
			return nil, err
		}
		published, current, publishErr := s.publishForPhone(ctx, site, options, progress)
		s.mu.Unlock()
		if publishErr != nil {
			return nil, publishErr
		}
		site = current
		progress("hosting")
		token, err = s.phoneSessionAfterPublication(ctx, site)
		if err != nil {
			return nil, phoneFailure(502, err)
		}
		if err := s.waitPhoneReady(ctx, site.IPNS, token, published); err != nil {
			return nil, phoneFailure(409, err)
		}
	}
	progress("pairing")
	if _, err := s.currentPhoneSite(ctx, site, true); err != nil {
		return nil, err
	}
	if err := s.phoneAPI(ctx, "PUT", "/connection", token, map[string]bool{"enabled": true}, nil); err != nil {
		return nil, phoneFailure(502, err)
	}
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, phoneFailure(500, err)
	}
	pub := base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())
	var pairing struct {
		mobile.PairingInfo
		Capability string `json:"capability"`
	}
	if _, err := s.currentPhoneSite(ctx, site, true); err != nil {
		return nil, err
	}
	if err := s.phoneAPI(ctx, "POST", "/pairings", token, map[string]string{"senderPublicKey": pub}, &pairing); err != nil {
		return nil, phoneFailure(502, err)
	}
	if !phonePairingInfoValid(pairing.PairingInfo, origin, site.IPNS, pub) || !phoneOpaqueToken(pairing.Capability) {
		return nil, phoneFailure(502, errors.New("invalid phone connection response"))
	}
	link := origin + "/#pair=" + pairing.ID + "." + pairing.Capability
	if _, err := s.currentPhoneSite(ctx, site, true); err != nil {
		return nil, err
	}
	pairContext, pairCancel := context.WithCancel(context.Background())
	connection := &phoneConnection{SiteID: site.ID, Name: site.Name, Token: token, URL: link, Info: pairing.PairingInfo, Private: private, Context: pairContext, Cancel: pairCancel}
	pending := s.phoneConnections()
	pending.mu.Lock()
	for id, p := range pending.entries {
		if p.Info.ExpiresAt <= time.Now().Unix() {
			pending.removeConnection(id)
		}
	}
	pending.entries[pairing.ID] = connection
	pending.mu.Unlock()
	id := pairing.ID
	time.AfterFunc(time.Until(time.Unix(pairing.ExpiresAt, 0)), func() {
		pending.mu.Lock()
		pending.removeConnection(id)
		pending.mu.Unlock()
	})
	return &phoneConnectionResult{ID: pairing.ID, URL: link, ExpiresAt: pairing.ExpiresAt, State: pairing.State, IPNS: site.IPNS, Name: site.Name}, nil
}

type phoneReadiness struct {
	IPNS     string `json:"ipns"`
	Ready    bool   `json:"ready"`
	Reason   string `json:"reason"`
	CID      string `json:"cid"`
	Sequence string `json:"sequence"`
}

func (s *Server) phoneNeedsPublication(ctx context.Context, site *store.Site) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	head, err := s.Pub.InspectSite(ctx, publish.DefaultHost, site.IPNS)
	if errors.Is(err, publish.ErrHostNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("The hosted site could not be verified; no desktop changes were published. Retry when hosting is available: %w", err)
	}
	if head.Site == nil || head.Site.IPNS != site.IPNS {
		return false, errors.New("hosted site identity changed")
	}
	if !head.AcceptsParent {
		return false, errors.New("The hosting service needs an update before it can accept phone posts safely.")
	}
	if publish.HostOf(head.Site) != publish.DefaultHost {
		return false, errors.New("The published site's hosting destination differs from the phone service.")
	}
	if !head.Site.HostingEnabled() {
		return true, nil
	}
	var descriptor render.MobileDescriptor
	if json.Unmarshal(head.Site.Raw[render.MobileDescriptorKey], &descriptor) != nil || descriptor.Version != 1 {
		return true, nil
	}
	// A descriptor mismatch may be a custom template, not an outdated site.
	// Do not publish local edits automatically to repair an unknown mismatch.
	return false, nil
}

func (s *Server) currentPhoneSite(ctx context.Context, original *store.Site, hosted bool) (*store.Site, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err := s.Store.Site(original.ID)
	if err != nil || current.IPNS != original.IPNS || current.IsArchived() || publish.HostOf(current) != publish.DefaultHost || (hosted && !current.HostingEnabled()) || !s.Node.Keystore().Has(original.ID) {
		return nil, &phoneProblem{Status: 409, Code: "site_changed", Err: errors.New("This site's identity or hosting settings changed. Start Connect phone again.")}
	}
	return current, nil
}

func (s *Server) publishForPhone(ctx context.Context, original *store.Site, options phonePreparationOptions, progress func(string)) (publish.Result, *store.Site, error) {
	current, err := s.currentPhoneSite(ctx, original, false)
	if err != nil {
		return publish.Result{}, nil, err
	}
	if current.HostingEnabled() != original.HostingEnabled() {
		return publish.Result{}, nil, phoneFailure(409, errors.New("Hosting settings changed. Start Connect phone again."))
	}
	if !current.HostingEnabled() {
		if !options.EnableHosting || !options.AllowPublish {
			return publish.Result{}, nil, phoneFailure(409, errors.New("Hosting and publication require your permission."))
		}
		if err := current.SetStorage(store.StorageHosted); err != nil {
			return publish.Result{}, nil, err
		}
		if err := s.Store.SaveSite(current); err != nil {
			return publish.Result{}, nil, err
		}
	}
	progress("publishing")
	result, err := s.Pub.PublishHosted(ctx, current.ID, func(stage publish.HostedStage) {
		if stage == publish.HostedUploading || stage == publish.HostedVerifying {
			progress("hosting")
		} else {
			progress("publishing")
		}
	})
	if err != nil {
		problem := phoneFailure(502, fmt.Errorf("The hosted site could not be prepared: %w", err))
		switch {
		case errors.Is(err, publish.ErrHostedBootstrapNeedsPublish):
			problem.Code = "hosted_publication_required"
		case errors.Is(err, publish.ErrHostedParentUnsupported):
			problem.Code = "hosting_update_required"
		case errors.Is(err, publish.ErrHostedOutcomeUnknown):
			problem.Code = "publication_outcome_unknown"
		case errors.Is(err, publish.ErrPublishedElsewhere), errors.Is(err, publish.ErrSiteBusy):
			problem.Code = "publication_conflict"
		}
		return result, nil, problem
	}
	current, err = s.currentPhoneSite(ctx, current, true)
	return result, current, err
}

func (s *Server) phoneSessionAfterPublication(ctx context.Context, site *store.Site) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	for {
		token, err := s.phoneSession(ctx, site)
		if err == nil {
			return token, nil
		}
		var service *phoneServiceError
		if !errors.As(err, &service) || service.Code != "site_not_ready" {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("The publication reached hosting, but the phone service has not verified it yet. Keep this computer awake and retry Connect phone: %w", ctx.Err())
		case <-time.After(1500 * time.Millisecond):
		}
	}
}

// Publish can finish while a large first host upload continues in the
// publisher's existing queue. Do not pair against an old compatible head or
// claim the computer can sleep until the hosted service sees this publication.
// An unchanged re-publish can advance the local sequence without advancing the
// host's sequence: the host already has the exact content CID and settles the
// queued push. Matching content is therefore sufficient, as is a newer head.
func (s *Server) waitPhoneReady(ctx context.Context, ipns, token string, published publish.Result) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	reason := "The hosted publication is still uploading. Keep this computer awake and try connecting again."
	for {
		var readiness struct {
			IPNS     string `json:"ipns"`
			Ready    bool   `json:"ready"`
			Reason   string `json:"reason"`
			CID      string `json:"cid"`
			Sequence string `json:"sequence"`
		}
		err := s.phoneAPI(ctx, "GET", "/site", token, nil, &readiness)
		if err == nil {
			if readiness.IPNS != ipns {
				return errors.New("phone service returned a different site")
			}
			seq, parseErr := strconv.ParseUint(readiness.Sequence, 10, 64)
			matchingContent := published.CID != "" && readiness.CID == published.CID
			newerHead := readiness.CID != "" && seq > published.Sequence
			if parseErr == nil && (matchingContent || newerHead) {
				if readiness.Ready {
					return nil
				}
				if readiness.Reason != "" {
					return errors.New(readiness.Reason)
				}
				return errors.New("republish this site with a supported Croptop template")
			}
		}
		select {
		case <-ctx.Done():
			return errors.New(reason)
		case <-time.After(1500 * time.Millisecond):
		}
	}
}

func phonePairingInfoValid(info mobile.PairingInfo, origin, ipns, sender string) bool {
	now := time.Now().Unix()
	return phoneOpaqueToken(info.ID) && info.Origin == origin && info.IPNS == ipns && info.SenderPublicKey == sender && info.ExpiresAt > now && info.ExpiresAt <= now+660
}

func phoneOpaqueToken(value string) bool {
	b, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == value
}

// phonePending returns a copy so the HTTP exchange never holds the pending
// map lock. The private key is immutable and the final update checks its ID.
func (s *Server) phonePending(w http.ResponseWriter, r *http.Request) (*phoneConnection, bool) {
	if !mobilePhoneAllowed(w, r) {
		return nil, false
	}
	pending := s.phoneConnections()
	pending.mu.Lock()
	defer pending.mu.Unlock()
	p := pending.entries[r.PathValue("pairid")]
	if p != nil && p.Info.ExpiresAt <= time.Now().Unix() {
		pending.removeConnection(p.Info.ID)
		p = nil
	}
	if p == nil || p.SiteID != strings.ToUpper(r.PathValue("id")) {
		writeErr(w, 404, errors.New("connection expired; start Connect phone again"))
		return nil, false
	}
	copy := *p
	return &copy, true
}

// Caller holds the pending map lock. Cancelling the lifetime also invalidates
// copies already returned by phonePending, not only future lookups.
func (p *phoneConnections) removeConnection(id string) {
	if connection := p.entries[id]; connection != nil {
		if connection.Cancel != nil {
			connection.Cancel()
		}
		connection.Private = nil
		delete(p.entries, id)
	}
}

func (s *Server) cancelPhoneConnection(w http.ResponseWriter, r *http.Request) {
	if !mobilePhoneAllowed(w, r) {
		return
	}
	pending := s.phoneConnections()
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if p := pending.entries[r.PathValue("pairid")]; p != nil {
		if p.SiteID != strings.ToUpper(r.PathValue("id")) {
			writeErr(w, 404, errors.New("connection not found"))
			return
		}
		pending.removeConnection(p.Info.ID)
	}
	writeJSON(w, 200, map[string]string{"state": "cancelled"})
}

func (s *Server) phonePairingStatus(ctx context.Context, p *phoneConnection) (mobile.PairingInfo, error) {
	var info mobile.PairingInfo
	if err := s.phoneAPI(ctx, "GET", "/pairings/"+p.Info.ID, p.Token, nil, &info); err != nil {
		return info, err
	}
	if !phonePairingInfoValid(info, p.Info.Origin, p.Info.IPNS, p.Info.SenderPublicKey) || info.ID != p.Info.ID || info.ExpiresAt != p.Info.ExpiresAt {
		return info, errors.New("connection details changed; start Connect phone again")
	}
	return info, nil
}

func (s *Server) phoneConnectionStatus(w http.ResponseWriter, r *http.Request) {
	p, ok := s.phonePending(w, r)
	if !ok {
		return
	}
	info, err := s.phonePairingStatus(r.Context(), p)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	code := ""
	if info.ReceiverPublicKey != "" && p.Private != nil {
		_, code, err = mobile.PairingSecrets(p.Private, info.ReceiverPublicKey, info)
		if err != nil {
			writeErr(w, 502, errors.New("invalid phone connection key"))
			return
		}
	}
	if info.State == "ready" || info.State == "consumed" {
		pending := s.phoneConnections()
		pending.mu.Lock()
		if info.State == "consumed" {
			pending.removeConnection(p.Info.ID)
		} else if stored := pending.entries[p.Info.ID]; stored != nil {
			stored.Private = nil
		}
		pending.mu.Unlock()
	}
	writeJSON(w, 200, map[string]any{"state": info.State, "code": code, "name": p.Name, "ipns": info.IPNS, "expiresAt": info.ExpiresAt})
}

func (s *Server) confirmPhoneConnection(w http.ResponseWriter, r *http.Request) {
	p, ok := s.phonePending(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if p.Context != nil {
		stop := context.AfterFunc(p.Context, cancel)
		defer stop()
		if err := p.Context.Err(); err != nil {
			writeErr(w, 409, errors.New("connection cancelled; start again"))
			return
		}
	}
	var in struct {
		Code string `json:"code"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, errors.New("enter the code shown on your phone"))
		return
	}
	info, err := s.phonePairingStatus(ctx, p)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	if info.State == "ready" || info.State == "consumed" {
		writeJSON(w, 200, map[string]string{"state": info.State})
		return
	}
	if info.State != "claimed" || p.Private == nil {
		writeErr(w, 409, errors.New("open the connection link on your phone first"))
		return
	}
	_, code, err := mobile.PairingSecrets(p.Private, info.ReceiverPublicKey, info)
	if err != nil {
		writeErr(w, 502, errors.New("invalid phone connection key"))
		return
	}
	entered := strings.ReplaceAll(strings.TrimSpace(in.Code), " ", "")
	if subtle.ConstantTimeCompare([]byte(entered), []byte(code)) != 1 {
		writeErr(w, 409, errors.New("the codes do not match; check your phone or start a new connection"))
		return
	}
	// Recheck the actual saved policy and cancellation immediately before any
	// private material is read. A completed or already-dispatched transfer is
	// not revocable by closing the sheet.
	if p.Context != nil && p.Context.Err() != nil {
		writeErr(w, 409, errors.New("connection cancelled; start again"))
		return
	}
	if _, err := s.currentPhoneSite(ctx, &store.Site{ID: p.SiteID, IPNS: p.Info.IPNS}, true); err != nil {
		writePhoneProblem(w, err)
		return
	}
	pem, err := s.Node.Keystore().ExportPEM(p.SiteID)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	plaintext, err := json.Marshal(map[string]any{"version": 1, "origin": info.Origin, "ipns": info.IPNS, "name": p.Name, "pem": string(pem)})
	for i := range pem {
		pem[i] = 0
	}
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	nonce, ciphertext, err := mobile.EncryptPairing(p.Private, info, plaintext)
	for i := range plaintext {
		plaintext[i] = 0
	}
	if err != nil {
		writeErr(w, 500, errors.New("could not encrypt this connection"))
		return
	}
	if _, err := s.currentPhoneSite(ctx, &store.Site{ID: p.SiteID, IPNS: p.Info.IPNS}, true); err != nil {
		writePhoneProblem(w, err)
		return
	}
	if p.Context != nil && p.Context.Err() != nil {
		writeErr(w, 409, errors.New("connection cancelled; start again"))
		return
	}
	if err := s.phoneAPI(ctx, "POST", "/pairings/"+info.ID+"/complete", p.Token, map[string]string{"nonce": nonce, "ciphertext": ciphertext}, nil); err != nil {
		writeErr(w, 502, err)
		return
	}
	pending := s.phoneConnections()
	pending.mu.Lock()
	if stored := pending.entries[info.ID]; stored != nil {
		stored.Private = nil
	}
	pending.mu.Unlock()
	writeJSON(w, 200, map[string]string{"state": "ready"})
}

func (s *Server) phoneConnectionQR(w http.ResponseWriter, r *http.Request) {
	p, ok := s.phonePending(w, r)
	if !ok {
		return
	}
	png, err := qrcode.Encode(p.URL, qrcode.Medium, 320)
	if err != nil {
		writeErr(w, 500, errors.New("could not generate connection QR code"))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}

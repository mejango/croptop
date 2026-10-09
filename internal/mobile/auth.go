package mobile

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/ipfs"
)

type challenge struct {
	IPNS, Message string
	ExpiresAt     int64
}
type session struct {
	IPNS      string `json:"ipns"`
	ExpiresAt int64  `json:"expiresAt"`
}
type authState struct {
	Sessions    map[string]session `json:"sessions"`
	Connections map[string]bool    `json:"connections"`
}

type issuanceWindow struct {
	Started int64
	Count   int
}

func validSiteIdentity(name string) bool {
	if !strings.HasPrefix(name, "k51") || len(name) > 128 {
		return false
	}
	_, err := ipfs.PublicKeyOf(name)
	return err == nil
}

// siteAllowed owns the operator's identity policy. Configuration is immutable
// while serving; checking on every request invalidates persisted sessions when
// a restarted pilot removes a site, without a remote state lookup.
func (s *Server) siteAllowed(name string) bool {
	if s.allowedSites == nil {
		return true
	}
	_, ok := s.allowedSites[name]
	return ok
}

func (s *Server) trustedProxy(r *http.Request) bool {
	if s.ProxySecret == "" {
		return true
	}
	expected := sha256.Sum256([]byte(s.ProxySecret))
	actual := sha256.Sum256([]byte(r.Header.Get("X-Croptop-Mobile-Proxy")))
	return subtle.ConstantTimeCompare(expected[:], actual[:]) == 1
}

// RemoteAddr is the trusted socket peer. Untrusted forwarding headers may not
// choose a rate-limit bucket; the production edge adds its own end-user limits.
func (s *Server) allowIssuanceLocked(r *http.Request) bool {
	address, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		address = r.RemoteAddr
	}
	now := s.now().Unix()
	for key, window := range s.issuance {
		if now-window.Started >= int64(time.Minute.Seconds()) {
			delete(s.issuance, key)
		}
	}
	window, exists := s.issuance[address]
	if !exists {
		if len(s.issuance) >= 4096 {
			return false
		}
		window.Started = now
	}
	if window.Count >= 120 {
		return false
	}
	window.Count++
	s.issuance[address] = window
	return true
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) challengeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IPNS string `json:"ipns"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !validSiteIdentity(body.IPNS) {
		apiError(w, 400, "invalid_site", "Import a valid Ed25519 Croptop site key.")
		return
	}
	if !s.siteAllowed(body.IPNS) {
		apiError(w, 403, "site_not_allowed", "This site is not included in the phone publishing pilot.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowIssuanceLocked(r) {
		apiError(w, 429, "rate_limited", "Too many connection attempts from this address. Try again shortly.")
		return
	}
	now := s.now().Unix()
	pending := 0
	for id, c := range s.challenges {
		if c.ExpiresAt <= now {
			delete(s.challenges, id)
		} else if c.IPNS == body.IPNS {
			pending++
		}
	}
	if pending >= 4 {
		apiError(w, 429, "rate_limited", "This site has several pending connection attempts. Finish one or wait a few minutes.")
		return
	}
	if len(s.challenges) >= 2048 {
		apiError(w, 429, "busy", "Too many connection attempts. Try again shortly.")
		return
	}
	id := randomToken()
	expiry := s.now().Add(challengeLifetime).Unix()
	msg := fmt.Sprintf("croptop-mobile-session\n%s\n%s\n%s\n%d", s.Origin, body.IPNS, id, expiry)
	s.challenges[id] = challenge{IPNS: body.IPNS, Message: msg, ExpiresAt: expiry}
	writeJSON(w, 200, map[string]any{"id": id, "message": msg, "expiresAt": expiry})
}

func (s *Server) sessionHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID        string `json:"id"`
		Signature []byte `json:"signature"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowIssuanceLocked(r) {
		apiError(w, 429, "rate_limited", "Too many connection attempts from this address. Try again shortly.")
		return
	}
	c, ok := s.challenges[body.ID]
	delete(s.challenges, body.ID) // every attempt consumes the challenge
	if ok && !s.siteAllowed(c.IPNS) {
		apiError(w, 403, "site_not_allowed", "This site is not included in the phone publishing pilot.")
		return
	}
	if !ok || c.ExpiresAt <= s.now().Unix() || !ipfs.VerifyIPNS(c.IPNS, []byte(c.Message), body.Signature) {
		apiError(w, 401, "invalid_signature", "The connection signature expired or could not be verified. Reconnect to try again.")
		return
	}
	if s.RequireHostedSite {
		select {
		case s.enrollments <- struct{}{}:
		default:
			apiError(w, 429, "busy", "Several sites are connecting. Reconnect to try again shortly.")
			return
		}
		// A proven key alone must not allocate durable public-service state.
		// Do not hold the authentication mutex during bounded upstream I/O:
		// existing connections and readiness remain available while it runs.
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		_, err := s.inspect(ctx, c.IPNS)
		cancel()
		<-s.enrollments
		s.mu.Lock()
		if err != nil {
			apiError(w, 409, "site_not_ready", "This site is not ready for phone posting: "+err.Error()+" Reconnect after resolving this, or retry if the host is temporarily unavailable.")
			return
		}
		if s.closed {
			apiError(w, 503, "unavailable", "Phone publishing is shutting down. Reconnect shortly.")
			return
		}
	}
	for hash, sess := range s.auth.Sessions {
		if sess.ExpiresAt <= s.now().Unix() {
			delete(s.auth.Sessions, hash)
		}
	}
	count := 0
	oldest := ""
	earliest := int64(1<<63 - 1)
	for hash, sess := range s.auth.Sessions {
		if sess.IPNS == c.IPNS {
			count++
			if sess.ExpiresAt < earliest {
				oldest = hash
				earliest = sess.ExpiresAt
			}
		}
	}
	// Renewing locally may retire this site's oldest service-only credential;
	// it never revokes or replaces any IPNS publishing key.
	if count >= 16 {
		delete(s.auth.Sessions, oldest)
	}
	// Bound persistent service credentials even for a valid root-key holder.
	if len(s.auth.Sessions) >= 10000 {
		apiError(w, 429, "busy", "The service has reached its connection limit.")
		return
	}
	token := randomToken()
	hash := digest([]byte(token))
	expiry := s.now().Add(sessionLifetime).Unix()
	s.auth.Sessions[hash] = session{IPNS: c.IPNS, ExpiresAt: expiry}
	if err := s.saveAuthLocked(); err != nil {
		delete(s.auth.Sessions, hash)
		apiError(w, 500, "storage", "Could not save this connection. Try again.")
		return
	}
	writeJSON(w, 200, map[string]any{"token": token, "expiresAt": expiry})
}

func (s *Server) authenticate(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") || len(h) > 128 {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.auth.Sessions[digest([]byte(strings.TrimPrefix(h, "Bearer ")))]
	if !ok || sess.ExpiresAt <= s.now().Unix() || !s.siteAllowed(sess.IPNS) {
		return ""
	}
	return sess.IPNS
}

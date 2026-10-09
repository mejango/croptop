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
	"github.com/mejango/croptop/internal/store"
	qrcode "github.com/skip2/go-qrcode"
)

// A pilot may select its dedicated trusted origin at build time without
// changing the stable default or requiring GUI users to set shell flags.
// -ldflags '-X github.com/mejango/croptop/internal/server.defaultMobileOrigin=https://…'
var defaultMobileOrigin = "https://app.crop.top"

type phoneConnection struct {
	SiteID, Name, Token, URL string
	Info                     mobile.PairingInfo
	Private                  *ecdh.PrivateKey
}

type phoneConnections struct {
	mu      sync.Mutex
	entries map[string]*phoneConnection
}

func (s *Server) phoneConnections() *phoneConnections {
	s.phoneOnce.Do(func() { s.phone = &phoneConnections{entries: make(map[string]*phoneConnection)} })
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
	mux.HandleFunc("GET /v0/croptop/sites/{id}/phone/{pairid}", s.phoneConnectionStatus)
	mux.HandleFunc("GET /v0/croptop/sites/{id}/phone/{pairid}/qr", s.phoneConnectionQR)
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
		return errors.New(problem.Error)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, reader)
		return err
	}
	return json.NewDecoder(reader).Decode(out)
}

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
	s.mu.Lock()
	defer s.mu.Unlock()
	site, ok := s.site(w, r)
	if !ok {
		return
	}
	if s.Node == nil || s.Pub == nil || !s.Node.Keystore().Has(site.ID) {
		writeErr(w, 409, errors.New("connect from the publisher that has this site's key"))
		return
	}
	if publish.HostOf(site) != publish.DefaultHost {
		writeErr(w, 409, errors.New("phone posting currently supports sites hosted on crop.top"))
		return
	}
	if !site.HostingEnabled() && !in.EnableHosting {
		writeJSON(w, 409, map[string]string{"error": "Allow crop.top to host this site so your phone can publish while this computer sleeps.", "code": "hosting_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	var config struct {
		Enabled bool   `json:"enabled"`
		Origin  string `json:"origin"`
		Version int    `json:"version"`
	}
	if err := s.phoneAPI(ctx, "GET", "/config", "", nil, &config); err != nil {
		writeErr(w, 502, err)
		return
	}
	origin, _ := s.mobileOrigin()
	if !config.Enabled || config.Version != 1 || config.Origin != origin {
		writeErr(w, 503, errors.New("phone posting is not enabled at this service yet"))
		return
	}
	if !site.HostingEnabled() {
		if err := site.SetStorage(store.StorageHosted); err != nil {
			writeErr(w, 500, err)
			return
		}
		if err := s.Store.SaveSite(site); err != nil {
			writeErr(w, 500, err)
			return
		}
	}
	// Bootstrap the signed compatibility descriptor and hosted policy using the
	// existing publisher; normal conflict/catch-up behavior remains its owner.
	published, err := s.Pub.Publish(ctx, site.ID, false)
	if err != nil {
		writeErr(w, 502, fmt.Errorf("publish this site before connecting your phone: %w", err))
		return
	}
	site, err = s.Store.Site(site.ID)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	token, err := s.phoneSession(ctx, site)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	if err := s.waitPhoneReady(ctx, site.IPNS, token, published); err != nil {
		writeErr(w, 409, err)
		return
	}
	if err := s.phoneAPI(ctx, "PUT", "/connection", token, map[string]bool{"enabled": true}, nil); err != nil {
		writeErr(w, 502, err)
		return
	}
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	pub := base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())
	var pairing struct {
		mobile.PairingInfo
		Capability string `json:"capability"`
	}
	if err := s.phoneAPI(ctx, "POST", "/pairings", token, map[string]string{"senderPublicKey": pub}, &pairing); err != nil {
		writeErr(w, 502, err)
		return
	}
	if !phonePairingInfoValid(pairing.PairingInfo, origin, site.IPNS, pub) || !phoneOpaqueToken(pairing.Capability) {
		writeErr(w, 502, errors.New("invalid phone connection response"))
		return
	}
	link := origin + "/#pair=" + pairing.ID + "." + pairing.Capability
	connection := &phoneConnection{SiteID: site.ID, Name: site.Name, Token: token, URL: link, Info: pairing.PairingInfo, Private: private}
	pending := s.phoneConnections()
	pending.mu.Lock()
	for id, p := range pending.entries {
		if p.Info.ExpiresAt <= time.Now().Unix() {
			delete(pending.entries, id)
		}
	}
	pending.entries[pairing.ID] = connection
	pending.mu.Unlock()
	id := pairing.ID
	time.AfterFunc(time.Until(time.Unix(pairing.ExpiresAt, 0)), func() {
		pending.mu.Lock()
		delete(pending.entries, id)
		pending.mu.Unlock()
	})
	writeJSON(w, 201, map[string]any{"id": pairing.ID, "url": link, "expiresAt": pairing.ExpiresAt, "state": pairing.State, "ipns": site.IPNS, "name": site.Name})
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
		delete(pending.entries, p.Info.ID)
		p = nil
	}
	if p == nil || p.SiteID != strings.ToUpper(r.PathValue("id")) {
		writeErr(w, 404, errors.New("connection expired; start Connect phone again"))
		return nil, false
	}
	copy := *p
	return &copy, true
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
			delete(pending.entries, p.Info.ID)
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
	var in struct {
		Code string `json:"code"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, errors.New("enter the code shown on your phone"))
		return
	}
	info, err := s.phonePairingStatus(r.Context(), p)
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
	if err := s.phoneAPI(r.Context(), "POST", "/pairings/"+info.ID+"/complete", p.Token, map[string]string{"nonce": nonce, "ciphertext": ciphertext}, nil); err != nil {
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

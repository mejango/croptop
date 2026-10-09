package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/mobile"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/store"
)

func phoneTestServer(t *testing.T) (*Server, *store.Site, http.Handler) {
	t.Helper()
	root := t.TempDir()
	node := ipfs.NewNode("unused-ipfs", filepath.Join(root, "ipfs"))
	site := &store.Site{ID: "F8E22F61-CA22-4538-9A88-6E3816EFAD87", Name: "My phone site"}
	name, err := node.Keystore().Generate(site.ID)
	if err != nil {
		t.Fatal(err)
	}
	site.IPNS = name
	s := &Server{Store: &store.Store{Root: root}, Node: node, Pub: &publish.Publisher{}}
	if err := s.Store.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.routesMobilePhone(mux)
	return s, site, mux
}

func phoneRequest(t *testing.T, h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	r.Header.Set("X-Croptop-Phone", "1")
	r.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPhoneConnectionRequiresExplicitHostingAndTrustedPage(t *testing.T) {
	s, site, handler := phoneTestServer(t)
	path := "/v0/croptop/sites/" + site.ID + "/phone"
	for _, tc := range []struct {
		name    string
		headers map[string]string
		status  int
	}{
		{"missing header", map[string]string{"X-Croptop-Phone": ""}, 403},
		{"foreign origin", map[string]string{"Origin": "https://example.org"}, 403},
		{"opaque preview", map[string]string{"Origin": "null"}, 403},
		{"same site other origin", map[string]string{"Sec-Fetch-Site": "same-site"}, 403},
		{"author page", map[string]string{"Referer": "http://localhost/" + site.ID + "/"}, 403},
		{"form post", map[string]string{"Content-Type": "text/plain"}, 415},
		{"explicit consent", nil, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := phoneRequest(t, handler, "POST", path, `{"enableHosting":false}`, tc.headers)
			if w.Code != tc.status {
				t.Fatalf("%d: %s", w.Code, w.Body)
			}
		})
	}
	saved, _ := s.Store.Site(site.ID)
	if saved.HostingEnabled() {
		t.Fatal("rejected request opted site into hosting")
	}
	publish.SetHost(site, "https://another-host.example")
	if err := s.Store.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	w := phoneRequest(t, handler, "POST", path, `{"enableHosting":true}`, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "crop.top") {
		t.Fatalf("custom host: %d %s", w.Code, w.Body)
	}
}

func TestPhoneConnectionUnavailableServiceDoesNotChangeStorage(t *testing.T) {
	s, site, handler := phoneTestServer(t)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"version": 1, "enabled": false, "origin": "https://app.crop.top"})
	}))
	defer service.Close()
	s.MobileOrigin = service.URL
	w := phoneRequest(t, handler, "POST", "/v0/croptop/sites/"+site.ID+"/phone", `{"enableHosting":true}`, nil)
	if w.Code != 503 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	saved, _ := s.Store.Site(site.ID)
	if saved.HostingEnabled() {
		t.Fatal("unavailable service changed site storage")
	}
}

func TestPhonePairingConfirmationEncryptsAndQRIsPrivate(t *testing.T) {
	s, site, handler := phoneTestServer(t)
	if err := site.SetStorage(store.StorageHosted); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	var relay *mobile.PairingRelay
	var completionBodies [][]byte
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/complete") {
			b, _ := io.ReadAll(r.Body)
			completionBodies = append(completionBodies, b)
			r.Body = io.NopCloser(bytes.NewReader(b))
		}
		relay.ServeHTTP(w, r, site.IPNS)
	}))
	defer service.Close()
	s.MobileOrigin = service.URL
	relay = mobile.NewPairingRelay(service.URL)
	sender, _ := ecdh.P256().GenerateKey(rand.Reader)
	receiver, _ := ecdh.P256().GenerateKey(rand.Reader)
	var created struct {
		mobile.PairingInfo
		Capability string `json:"capability"`
	}
	if err := s.phoneAPI(context.Background(), "POST", "/pairings", "session", map[string]string{"senderPublicKey": base64.StdEncoding.EncodeToString(sender.PublicKey().Bytes())}, &created); err != nil {
		t.Fatal(err)
	}
	capability := created.Capability
	receiverRequest := func(action string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", service.URL+"/v0/mobile/pairings/"+created.ID+"/"+action, bytes.NewReader(b))
		r.Header.Set("X-Croptop-Pairing", capability)
		w := httptest.NewRecorder()
		relay.ServeHTTP(w, r, "")
		return w
	}
	claim := receiverRequest("claim", map[string]string{"receiverPublicKey": base64.StdEncoding.EncodeToString(receiver.PublicKey().Bytes())})
	var info mobile.PairingInfo
	if err := json.Unmarshal(claim.Body.Bytes(), &info); err != nil || claim.Code != 200 {
		t.Fatalf("claim %d %s %v", claim.Code, claim.Body, err)
	}
	key, code, err := mobile.PairingSecrets(receiver, info.SenderPublicKey, info)
	if err != nil {
		t.Fatal(err)
	}
	s.phoneConnections().entries[info.ID] = &phoneConnection{SiteID: site.ID, Name: site.Name, Token: "session", URL: service.URL + "/#pair=" + info.ID + "." + capability, Info: created.PairingInfo, Private: sender}
	path := "/v0/croptop/sites/" + site.ID + "/phone/" + info.ID
	w := phoneRequest(t, handler, "GET", path+"/qr", "", nil)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !bytes.HasPrefix(w.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("qr %d %s", w.Code, w.Body)
	}
	w = phoneRequest(t, handler, "GET", path+"/qr", "", map[string]string{"Origin": "https://evil.example"})
	if w.Code != 403 {
		t.Fatalf("foreign QR %d", w.Code)
	}
	w = phoneRequest(t, handler, "POST", path+"/confirm", `{"code":"wrong"}`, nil)
	if w.Code != 409 || len(completionBodies) != 0 {
		t.Fatalf("bad code transferred key: %d %s", w.Code, w.Body)
	}
	w = phoneRequest(t, handler, "POST", strings.Replace(path, site.ID, "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", 1)+"/confirm", `{"code":"`+code+`"}`, nil)
	if w.Code != 404 {
		t.Fatalf("wrong local site %d", w.Code)
	}
	w = phoneRequest(t, handler, "POST", path+"/confirm", `{"code":"`+code+`"}`, nil)
	if w.Code != 200 || len(completionBodies) != 1 {
		t.Fatalf("confirm %d %s", w.Code, w.Body)
	}
	if bytes.Contains(completionBodies[0], []byte("PRIVATE KEY")) || bytes.Contains(completionBodies[0], []byte("pem")) {
		t.Fatal("plaintext key sent to service")
	}
	consumed := receiverRequest("consume", map[string]bool{"confirmed": true})
	var envelope mobile.PairingInfo
	if err := json.Unmarshal(consumed.Body.Bytes(), &envelope); err != nil || consumed.Code != 200 {
		t.Fatalf("consume %d %s %v", consumed.Code, consumed.Body, err)
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	nonce, _ := base64.StdEncoding.DecodeString(envelope.Nonce)
	encrypted, _ := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	plaintext, err := aead.Open(nil, nonce, encrypted, mobile.PairingTranscript(info))
	if err != nil {
		t.Fatal(err)
	}
	var received struct {
		PEM, IPNS, Origin, Name string
		Version                 int
	}
	if err := json.Unmarshal(plaintext, &received); err != nil {
		t.Fatal(err)
	}
	expected, _ := s.Node.Keystore().ExportPEM(site.ID)
	if received.PEM != string(expected) || received.IPNS != site.IPNS || received.Name != site.Name || received.Origin != service.URL || received.Version != 1 {
		t.Fatalf("unexpected decrypted envelope: %+v", received)
	}
	w = phoneRequest(t, handler, "GET", path, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "consumed") {
		t.Fatalf("status %d %s", w.Code, w.Body)
	}
	w = phoneRequest(t, handler, "GET", path+"/qr", "", nil)
	if w.Code != 404 {
		t.Fatal("consumed local pairing retained key")
	}
}

func TestPhoneExpiredPendingAndServiceOriginValidation(t *testing.T) {
	s, site, h := phoneTestServer(t)
	s.phoneConnections().entries["expired"] = &phoneConnection{SiteID: site.ID, Info: mobile.PairingInfo{ID: "expired", ExpiresAt: time.Now().Add(-time.Second).Unix()}}
	w := phoneRequest(t, h, "GET", "/v0/croptop/sites/"+site.ID+"/phone/expired", "", nil)
	if w.Code != 404 {
		t.Fatal("expired pending connection remained accessible")
	}
	for _, origin := range []string{"http://remote.example", "https://app.crop.top/path", "https://user:pass@app.crop.top", "https://app.crop.top?secret=yes"} {
		s.MobileOrigin = origin
		if _, err := s.mobileOrigin(); err == nil {
			t.Fatalf("accepted origin %s", origin)
		}
	}
	for _, origin := range []string{"https://app.crop.top", "http://127.0.0.1:1234", "http://[::1]:1234"} {
		s.MobileOrigin = origin
		if _, err := s.mobileOrigin(); err != nil {
			t.Fatalf("rejected origin %s: %v", origin, err)
		}
	}
}

func TestPhoneDefaultOriginAndExplicitOverrides(t *testing.T) {
	const deployedOrigin = "https://croptop-phone-923c1bafd14ea328.croptop.workers.dev"
	if defaultMobileOrigin != deployedOrigin {
		t.Fatalf("ordinary desktop build defaults to %q instead of deployed trusted origin", defaultMobileOrigin)
	}
	for _, override := range []string{"", "https://isolated-composer.example", "http://127.0.0.1:18090"} {
		s := &Server{MobileOrigin: override}
		want := override
		if want == "" {
			want = deployedOrigin
		}
		got, err := s.mobileOrigin()
		if err != nil || got != want {
			t.Fatalf("override %q: origin %q, error %v; want %q", override, got, err, want)
		}
		if s.MobileOrigin != override {
			t.Fatal("origin resolution rewrote explicit configuration")
		}
	}
	// Keep the existing link-time override mechanism used by isolated builds;
	// an explicit runtime origin must still win over that build's default.
	before := defaultMobileOrigin
	defer func() { defaultMobileOrigin = before }()
	defaultMobileOrigin = "https://build-specific-composer.example"
	if got, err := (&Server{}).mobileOrigin(); err != nil || got != defaultMobileOrigin {
		t.Fatalf("build default override lost: %q, %v", got, err)
	}
	if got, err := (&Server{MobileOrigin: deployedOrigin}).mobileOrigin(); err != nil || got != deployedOrigin {
		t.Fatalf("runtime origin must override build default: %q, %v", got, err)
	}
}

func TestPhoneSessionRejectsSigningArbitraryServiceBytes(t *testing.T) {
	s, site, _ := phoneTestServer(t)
	requests := 0
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		writeJSON(w, 200, map[string]any{"id": "test", "message": "sign this arbitrary proposal", "expiresAt": time.Now().Add(time.Minute).Unix()})
	}))
	defer service.Close()
	s.MobileOrigin = service.URL
	if _, err := s.phoneSession(context.Background(), site); err == nil {
		t.Fatal("arbitrary server challenge signed")
	}
	if requests != 1 {
		t.Fatalf("sent signature after bad challenge: %d", requests)
	}
}

func TestPhoneReadyRequiresHostedBootstrapContentOrNewerHead(t *testing.T) {
	s, site, _ := phoneTestServer(t)
	const bootstrapCID = "bafy-bootstrap-content"
	for _, test := range []struct {
		name, cid, sequence, ipns, reason string
		ready                             bool
		wantError                         string
	}{
		{"unchanged republish retains lower host sequence", bootstrapCID, "1", site.IPNS, "", true, ""},
		{"exact bootstrap", bootstrapCID, "2", site.IPNS, "", true, ""},
		{"same content newer sequence", bootstrapCID, "3", site.IPNS, "", true, ""},
		{"newer ready publication", "bafy-newer-content", "3", site.IPNS, "", true, ""},
		{"older compatible publication", "bafy-older-content", "1", site.IPNS, "", true, "still uploading"},
		{"conflicting same sequence", "bafy-conflicting-content", "2", site.IPNS, "", true, "still uploading"},
		{"wrong site with matching content", bootstrapCID, "2", "another-site", "", true, "different site"},
		{"matching content is not ready", bootstrapCID, "1", site.IPNS, "Hosting is disabled.", false, "Hosting is disabled."},
		{"newer publication is not ready", "bafy-newer-content", "3", site.IPNS, "Template is unsupported.", false, "Template is unsupported."},
		{"matching content malformed sequence", bootstrapCID, "invalid", site.IPNS, "", true, "still uploading"},
		{"newer sequence without content identity", "", "3", site.IPNS, "", true, "still uploading"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, map[string]any{"ipns": test.ipns, "ready": test.ready, "reason": test.reason, "cid": test.cid, "sequence": test.sequence})
			}))
			defer service.Close()
			s.MobileOrigin = service.URL
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			err := s.waitPhoneReady(ctx, site.IPNS, "session", publish.Result{CID: bootstrapCID, Sequence: 2})
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error %v, want %q", err, test.wantError)
			}
		})
	}
}

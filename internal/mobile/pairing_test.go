package mobile

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestPairingCrossClientFixture(t *testing.T) {
	b, err := os.ReadFile("../../testdata/mobile-pairing-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		SenderPrivateKey, ReceiverPrivateKey, Transcript, AESKey, ConfirmationCode, Nonce, Ciphertext, Plaintext string
		Info                                                                                                     PairingInfo
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	decode := func(s string) []byte {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	sender, err := ecdh.P256().NewPrivateKey(decode(f.SenderPrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := ecdh.P256().NewPrivateKey(decode(f.ReceiverPrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(PairingTranscript(f.Info)); got != f.Transcript {
		t.Fatalf("transcript mismatch %q", got)
	}
	for _, pair := range []struct {
		private *ecdh.PrivateKey
		peer    string
	}{{sender, f.Info.ReceiverPublicKey}, {receiver, f.Info.SenderPublicKey}} {
		key, code, err := PairingSecrets(pair.private, pair.peer, f.Info)
		if err != nil || !bytes.Equal(key, decode(f.AESKey)) || code != f.ConfirmationCode {
			t.Fatalf("fixture secrets: key=%x code=%s err=%v", key, code, err)
		}
	}
	block, _ := aes.NewCipher(decode(f.AESKey))
	aead, _ := cipher.NewGCM(block)
	plaintext, err := aead.Open(nil, decode(f.Nonce), decode(f.Ciphertext), PairingTranscript(f.Info))
	if err != nil || string(plaintext) != f.Plaintext {
		t.Fatalf("fixture decrypt: %v %s", err, plaintext)
	}
	nonce, ciphertext, err := EncryptPairing(sender, f.Info, []byte(f.Plaintext))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err = aead.Open(nil, decode(nonce), decode(ciphertext), PairingTranscript(f.Info))
	if err != nil || string(plaintext) != f.Plaintext {
		t.Fatalf("roundtrip: %v", err)
	}
	changed := f.Info
	changed.IPNS += "other"
	if _, err := aead.Open(nil, decode(nonce), decode(ciphertext), PairingTranscript(changed)); err == nil {
		t.Fatal("cross-site envelope accepted")
	}
	changed = f.Info
	changed.Origin = "https://attacker.example"
	if _, err := aead.Open(nil, decode(nonce), decode(ciphertext), PairingTranscript(changed)); err == nil {
		t.Fatal("cross-origin envelope accepted")
	}
}

type pairingBlockedBody struct {
	reading chan struct{}
	release chan struct{}
}

func (b *pairingBlockedBody) Read([]byte) (int, error) {
	close(b.reading)
	<-b.release
	return 0, io.EOF
}
func (b *pairingBlockedBody) Close() error { return nil }

type pairingBlockedWriter struct {
	*httptest.ResponseRecorder
	writing chan struct{}
	release chan struct{}
}

func (w *pairingBlockedWriter) Write(b []byte) (int, error) {
	close(w.writing)
	<-w.release
	return w.ResponseRecorder.Write(b)
}

func TestPairingSlowIOCannotBlockOtherSites(t *testing.T) {
	for _, direction := range []string{"request body", "response reader"} {
		t.Run(direction, func(t *testing.T) {
			relay := NewPairingRelay("https://app.crop.top")
			relay.entries["first"] = &pairingEntry{PairingInfo: PairingInfo{ID: "first", IPNS: "first-site", ExpiresAt: time.Now().Add(time.Minute).Unix(), State: "waiting"}}
			relay.entries["second"] = &pairingEntry{PairingInfo: PairingInfo{ID: "second", IPNS: "second-site", ExpiresAt: time.Now().Add(time.Minute).Unix(), State: "waiting"}}
			blocked, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			defer close(release)
			r := httptest.NewRequest("GET", "/v0/mobile/pairings/first", nil)
			var w http.ResponseWriter = httptest.NewRecorder()
			if direction == "request body" {
				r.Method = "POST"
				r.URL.Path += "/complete"
				r.Body = &pairingBlockedBody{reading: blocked, release: release}
			} else {
				w = &pairingBlockedWriter{ResponseRecorder: httptest.NewRecorder(), writing: blocked, release: release}
			}
			go func() { relay.ServeHTTP(w, r, "first-site"); close(finished) }()
			select {
			case <-blocked:
			case <-time.After(time.Second):
				t.Fatal("slow client did not enter I/O")
			}
			other := make(chan int, 1)
			go func() {
				w := httptest.NewRecorder()
				relay.ServeHTTP(w, httptest.NewRequest("GET", "/v0/mobile/pairings/second", nil), "second-site")
				other <- w.Code
			}()
			select {
			case code := <-other:
				if code != 200 {
					t.Fatalf("status %d", code)
				}
			case <-time.After(time.Second):
				t.Fatal("one slow client blocked another site's pairing")
			}
		})
	}
}

func TestPairingRelayOneTimeSiteBoundAndExpires(t *testing.T) {
	relay := NewPairingRelay("https://app.crop.top")
	now := time.Unix(2000000000, 0)
	relay.now = func() time.Time { return now }
	request := func(method, path, site, cap string, body any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "https://app.crop.top/v0/mobile/pairings"+path, bytes.NewReader(b))
		if cap != "" {
			r.Header.Set("X-Croptop-Pairing", cap)
		}
		w := httptest.NewRecorder()
		relay.ServeHTTP(w, r, site)
		var data map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &data)
		return w.Code, data
	}
	sender, _ := ecdh.P256().GenerateKey(rand.Reader)
	receiver, _ := ecdh.P256().GenerateKey(rand.Reader)
	other, _ := ecdh.P256().GenerateKey(rand.Reader)
	pub := func(k *ecdh.PrivateKey) string { return base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()) }
	if status, _ := request("POST", "", "", "", map[string]string{"senderPublicKey": pub(sender)}); status != 401 {
		t.Fatalf("unauth start %d", status)
	}
	status, data := request("POST", "", "site-A", "", map[string]string{"senderPublicKey": pub(sender)})
	if status != 201 {
		t.Fatal(status, data)
	}
	id, cap := data["id"].(string), data["capability"].(string)
	if len(cap) != 43 || len(id) != 43 || id == cap {
		t.Fatal("invalid capability")
	}
	if status, _ := request("GET", "/"+id, "site-B", "", nil); status != 403 {
		t.Fatalf("cross site read %d", status)
	}
	if status, _ := request("POST", "/"+id+"/claim", "", "wrong", map[string]string{"receiverPublicKey": pub(receiver)}); status != 403 {
		t.Fatalf("wrong capability %d", status)
	}
	for n := 0; n < 2; n++ {
		if status, _ := request("POST", "/"+id+"/claim", "", cap, map[string]string{"receiverPublicKey": pub(receiver)}); status != 200 {
			t.Fatalf("claim/retry %d", status)
		}
	}
	if status, _ := request("POST", "/"+id+"/claim", "", cap, map[string]string{"receiverPublicKey": pub(other)}); status != 409 {
		t.Fatalf("second receiver %d", status)
	}
	if status, _ := request("POST", "/"+id+"/consume", "", cap, map[string]bool{"confirmed": false}); status != 400 {
		t.Fatalf("unconfirmed %d", status)
	}
	if status, data := request("POST", "/"+id+"/consume", "", cap, map[string]bool{"confirmed": true}); status != 409 || data["code"] != "pairing_pending" {
		t.Fatalf("pending %d %v", status, data)
	}
	body := map[string]string{"nonce": base64.StdEncoding.EncodeToString(make([]byte, 12)), "ciphertext": base64.StdEncoding.EncodeToString(make([]byte, 24))}
	if status, _ := request("POST", "/"+id+"/complete", "site-B", "", body); status != 403 {
		t.Fatalf("cross site complete %d", status)
	}
	for n := 0; n < 2; n++ {
		if status, _ := request("POST", "/"+id+"/complete", "site-A", "", body); status != 200 {
			t.Fatalf("complete/retry %d", status)
		}
	}
	body["ciphertext"] = base64.StdEncoding.EncodeToString(make([]byte, 25))
	if status, _ := request("POST", "/"+id+"/complete", "site-A", "", body); status != 409 {
		t.Fatalf("replace envelope %d", status)
	}
	if status, data := request("GET", "/"+id, "site-A", "", nil); status != 200 || data["ciphertext"] != nil {
		t.Fatalf("status leaked envelope %d %v", status, data)
	}
	if status, data := request("POST", "/"+id+"/consume", "", cap, map[string]bool{"confirmed": true}); status != 200 || data["ciphertext"] == nil {
		t.Fatalf("consume %d %v", status, data)
	}
	if relay.entries[id].Ciphertext != "" || relay.entries[id].Nonce != "" || relay.entries[id].capabilityHash != [32]byte{} {
		t.Fatal("consumed envelope retained")
	}
	if status, _ := request("POST", "/"+id+"/consume", "", cap, map[string]bool{"confirmed": true}); status != 410 {
		t.Fatalf("replay %d", status)
	}
	if status, _ := request("POST", "/"+id+"/complete", "site-A", "", body); status != 409 {
		t.Fatalf("resurrect consumed %d", status)
	}
	_, data = request("POST", "", "site-A", "", map[string]string{"senderPublicKey": pub(sender)})
	id, cap = data["id"].(string), data["capability"].(string)
	now = now.Add(10 * time.Minute)
	if status, _ := request("POST", "/"+id+"/claim", "", cap, map[string]string{"receiverPublicKey": pub(receiver)}); status != 404 {
		t.Fatalf("expired claim %d", status)
	}
	if len(relay.entries) != 0 {
		t.Fatal("expired entries retained")
	}
}

func TestPairingRejectsMalformedKeysAndBoundsSessions(t *testing.T) {
	relay := NewPairingRelay("https://app.crop.top")
	for _, key := range []string{"", "not-base64", base64.StdEncoding.EncodeToString(make([]byte, 65))} {
		b, _ := json.Marshal(map[string]string{"senderPublicKey": key})
		r := httptest.NewRequest("POST", "/v0/mobile/pairings", bytes.NewReader(b))
		w := httptest.NewRecorder()
		relay.ServeHTTP(w, r, "site")
		if w.Code != 400 {
			t.Fatalf("malformed key accepted %q: %d", key, w.Code)
		}
	}
	key, _ := ecdh.P256().GenerateKey(rand.Reader)
	for i := 0; i < 5; i++ {
		b, _ := json.Marshal(map[string]string{"senderPublicKey": base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())})
		r := httptest.NewRequest("POST", "/v0/mobile/pairings", bytes.NewReader(b))
		w := httptest.NewRecorder()
		relay.ServeHTTP(w, r, "site")
		want := 201
		if i == 4 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("session %d: status %d want %d", i, w.Code, want)
		}
	}
}

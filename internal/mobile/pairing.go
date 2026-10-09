package mobile

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PairingInfo contains only public transcript material. The relay has no key
// material with which to decrypt the optional single-use envelope.
type PairingInfo struct {
	ID                string `json:"id"`
	Origin            string `json:"origin"`
	IPNS              string `json:"ipns"`
	SenderPublicKey   string `json:"senderPublicKey"`
	ReceiverPublicKey string `json:"receiverPublicKey,omitempty"`
	ExpiresAt         int64  `json:"expiresAt"`
	State             string `json:"state"`
	Nonce             string `json:"nonce,omitempty"`
	Ciphertext        string `json:"ciphertext,omitempty"`
}

type pairingEntry struct {
	PairingInfo
	capabilityHash [32]byte
}

// PairingRelay is deliberately ephemeral: only the encrypted transfer lives
// here, for at most ten minutes. Restarting safely cancels pending transfers.
type PairingRelay struct {
	mu      sync.Mutex
	origin  string
	entries map[string]*pairingEntry
	now     func() time.Time
}

func NewPairingRelay(origin string) *PairingRelay {
	return &PairingRelay{origin: strings.TrimRight(origin, "/"), entries: make(map[string]*pairingEntry), now: time.Now}
}

func pairingRandom() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func pairingPublicKey(value string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(value)
	if err != nil || base64.StdEncoding.EncodeToString(b) != value {
		return nil, errors.New("invalid public key")
	}
	if _, err = ecdh.P256().NewPublicKey(b); err != nil {
		return nil, errors.New("invalid P-256 public key")
	}
	return b, nil
}

func pairingJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func pairingError(w http.ResponseWriter, status int, code, message string) {
	pairingJSON(w, status, map[string]string{"code": code, "error": message})
}

func pairingDecode(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		pairingError(w, 400, "pairing_input", "Invalid pairing request.")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		pairingError(w, 400, "pairing_input", "Send exactly one pairing request.")
		return false
	}
	return true
}

type pairingInput struct {
	SenderPublicKey   string `json:"senderPublicKey"`
	ReceiverPublicKey string `json:"receiverPublicKey"`
	Nonce             string `json:"nonce"`
	Ciphertext        string `json:"ciphertext"`
	Confirmed         bool   `json:"confirmed"`
}

// ServeHTTP handles the /v0/mobile/pairings namespace. The caller authenticates
// ordinary site sessions and passes the resulting IPNS identity; only claim
// and consume instead authorize with a separate receiver capability.
func (p *PairingRelay) ServeHTTP(w http.ResponseWriter, r *http.Request, ipns string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	path := strings.TrimPrefix(r.URL.Path, "/v0/mobile/")
	parts := strings.Split(path, "/")
	if parts[0] != "pairings" || len(parts) > 3 {
		http.NotFound(w, r)
		return
	}
	var in pairingInput
	if r.Method == http.MethodPost && !pairingDecode(w, r, &in) {
		return
	}
	// Slow request bodies and slow readers must never hold the relay state
	// mutex. Only the in-memory transition below is serialized.
	status, value := p.apply(r.Method, parts, ipns, r.Header.Get("X-Croptop-Pairing"), in)
	pairingJSON(w, status, value)
}

func (p *PairingRelay) apply(method string, parts []string, ipns, capability string, in pairingInput) (status int, value any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	reply := func(code int, result any) { status, value = code, result }
	fail := func(code int, kind, message string) { reply(code, map[string]string{"code": kind, "error": message}) }
	now := p.now().Unix()
	for id, e := range p.entries {
		if now >= e.ExpiresAt {
			delete(p.entries, id)
		}
	}
	if len(parts) == 1 {
		if method != http.MethodPost {
			fail(405, "method", "Use POST to start a connection.")
			return
		}
		if ipns == "" {
			fail(401, "unauthorized", "Connect your site first.")
			return
		}
		if _, err := pairingPublicKey(in.SenderPublicKey); err != nil {
			fail(400, "pairing_key", err.Error())
			return
		}
		count := 0
		for _, e := range p.entries {
			if e.IPNS == ipns {
				count++
			}
		}
		if len(p.entries) >= 1024 || count >= 4 {
			fail(429, "pairing_limit", "Wait for the previous connection to expire.")
			return
		}
		id, cap := pairingRandom(), pairingRandom()
		e := &pairingEntry{PairingInfo: PairingInfo{ID: id, Origin: p.origin, IPNS: ipns, SenderPublicKey: in.SenderPublicKey, ExpiresAt: now + 600, State: "waiting"}, capabilityHash: sha256.Sum256([]byte(cap))}
		p.entries[id] = e
		time.AfterFunc(10*time.Minute, func() { p.mu.Lock(); delete(p.entries, id); p.mu.Unlock() })
		reply(201, struct {
			PairingInfo
			Capability string `json:"capability"`
		}{e.PairingInfo, cap})
		return
	}
	e := p.entries[parts[1]]
	if e == nil {
		fail(404, "pairing_missing", "This connection expired. Start a new connection on the computer.")
		return
	}
	action := ""
	if len(parts) == 3 {
		action = parts[2]
	}
	receiver := action == "claim" || action == "consume"
	if receiver {
		h := sha256.Sum256([]byte(capability))
		// Keep the tombstone to deny replay, but discard the actual capability.
		if e.State == "consumed" {
			fail(410, "pairing_consumed", "This connection was already used. Start a new connection if needed.")
			return
		}
		if capability == "" || subtle.ConstantTimeCompare(h[:], e.capabilityHash[:]) != 1 {
			fail(403, "pairing_capability", "Invalid connection link.")
			return
		}
	} else if ipns == "" || ipns != e.IPNS {
		fail(403, "pairing_site", "This connection belongs to another site.")
		return
	}
	if action == "" && method == http.MethodGet {
		info := e.PairingInfo
		info.Nonce, info.Ciphertext = "", ""
		reply(200, info)
		return
	}
	if method != http.MethodPost {
		fail(405, "method", "Use POST for this action.")
		return
	}
	switch action {
	case "claim":
		if _, err := pairingPublicKey(in.ReceiverPublicKey); err != nil {
			fail(400, "pairing_key", err.Error())
			return
		}
		if e.ReceiverPublicKey != "" && e.ReceiverPublicKey != in.ReceiverPublicKey {
			fail(409, "pairing_claimed", "Another device opened this connection. Start a new connection.")
			return
		}
		if e.State == "waiting" {
			e.ReceiverPublicKey, e.State = in.ReceiverPublicKey, "claimed"
		}
		info := e.PairingInfo
		info.Nonce, info.Ciphertext = "", ""
		reply(200, info)
	case "complete":
		if e.State == "ready" && in.Nonce == e.Nonce && in.Ciphertext == e.Ciphertext {
			reply(200, map[string]string{"state": e.State})
			return
		}
		if e.State != "claimed" {
			fail(409, "pairing_state", "Wait for the phone to show a confirmation code.")
			return
		}
		nonce, ne := base64.StdEncoding.DecodeString(in.Nonce)
		ciphertext, ce := base64.StdEncoding.DecodeString(in.Ciphertext)
		if ne != nil || ce != nil || len(nonce) != 12 || len(ciphertext) < 17 || len(ciphertext) > 2048 {
			fail(400, "pairing_envelope", "Invalid encrypted connection.")
			return
		}
		e.Nonce, e.Ciphertext, e.State = in.Nonce, in.Ciphertext, "ready"
		reply(200, map[string]string{"state": e.State})
	case "consume":
		if !in.Confirmed {
			fail(400, "pairing_confirmation", "Confirm the site and code first.")
			return
		}
		if e.State != "ready" {
			fail(409, "pairing_pending", "Confirm the matching code on your computer.")
			return
		}
		e.State = "consumed"
		info := e.PairingInfo
		e.Nonce, e.Ciphertext, e.capabilityHash = "", "", [32]byte{}
		reply(200, info)
	default:
		fail(404, "not_found", "Not found.")
	}
	return
}

func PairingTranscript(info PairingInfo) []byte {
	return []byte(strings.Join([]string{"croptop-pairing-v1", info.Origin, info.ID, info.IPNS, strconv.FormatInt(info.ExpiresAt, 10), info.SenderPublicKey, info.ReceiverPublicKey}, "\n"))
}

// PairingSecrets is shared by the existing publisher and conformance tests.
// The relay never calls it and never possesses an ephemeral private key.
func PairingSecrets(private *ecdh.PrivateKey, peer string, info PairingInfo) ([]byte, string, error) {
	b, err := pairingPublicKey(peer)
	if err != nil {
		return nil, "", err
	}
	pub, _ := ecdh.P256().NewPublicKey(b)
	secret, err := private.ECDH(pub)
	if err != nil {
		return nil, "", err
	}
	salt := sha256.Sum256(PairingTranscript(info))
	key, err := hkdf.Key(sha256.New, secret, salt[:], "croptop-pairing-key-v1", 32)
	if err != nil {
		return nil, "", err
	}
	code, err := hkdf.Key(sha256.New, secret, salt[:], "croptop-pairing-code-v1", 4)
	if err != nil {
		return nil, "", err
	}
	return key, fmt.Sprintf("%08d", binary.BigEndian.Uint32(code)%100000000), nil
}

func EncryptPairing(private *ecdh.PrivateKey, info PairingInfo, plaintext []byte) (nonce, ciphertext string, err error) {
	key, _, err := PairingSecrets(private, info.ReceiverPublicKey, info)
	if err != nil {
		return "", "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	n := make([]byte, aead.NonceSize())
	if _, err = rand.Read(n); err != nil {
		return "", "", err
	}
	encrypted := aead.Seal(nil, n, plaintext, PairingTranscript(info))
	return base64.StdEncoding.EncodeToString(n), base64.StdEncoding.EncodeToString(encrypted), nil
}

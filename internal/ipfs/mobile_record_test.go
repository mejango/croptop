package ipfs

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/ipfs/boxo/ipns"
	ipnspb "github.com/ipfs/boxo/ipns/pb"
	"github.com/ipfs/boxo/path"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"
)

const mobileFixtureCID = "bafkreigh2akiscaildcxk7zhwlm4conbh6x5xjvdcz5cmnz7x5e5ckihxa"

func mobileTestKey(t *testing.T, offset byte) (ed25519.PrivateKey, string) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i) + offset
	}
	private := ed25519.NewKeyFromSeed(seed)
	public, err := crypto.UnmarshalEd25519PublicKey(private.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return private, ipns.NameFromPeer(id).String()
}

func TestMobileRecordExternalSigning(t *testing.T) {
	private, name := mobileTestKey(t, 0)
	for _, seq := range []uint64{0, 42, math.MaxInt64} {
		t.Run(strconv.FormatUint(seq, 10), func(t *testing.T) {
			record, payload, err := NewUnsignedRecord(name, mobileFixtureCID, seq)
			if err != nil {
				t.Fatal(err)
			}
			unsigned, err := ipns.UnmarshalRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			identity, _ := ipns.NameFromString(name)
			if err := ipns.ValidateWithName(unsigned, identity); err == nil {
				t.Fatal("unsigned record must not validate")
			}
			signature := ed25519.Sign(private, payload)
			completed, err := CompleteRecord(name, record, signature)
			if err != nil {
				t.Fatal(err)
			}
			verified, err := ipns.UnmarshalRecord(completed)
			if err != nil {
				t.Fatal(err)
			}
			if err := ipns.ValidateWithName(verified, identity); err != nil {
				t.Fatal(err)
			}
			value, err := verified.Value()
			if err != nil || value.String() != "/ipfs/"+mobileFixtureCID {
				t.Fatalf("value %v: %v", value, err)
			}
			gotSequence, err := verified.Sequence()
			if err != nil || gotSequence != seq {
				t.Fatalf("sequence %d: %v", gotSequence, err)
			}
			ttl, err := verified.TTL()
			if err != nil || ttl != ipnsTTL {
				t.Fatalf("ttl %v: %v", ttl, err)
			}
			// A real Boxo signer must emit exactly the same completed record.
			validity, _ := verified.Validity()
			sk, _ := crypto.UnmarshalEd25519PrivateKey(private)
			c, _ := cid.Decode(mobileFixtureCID)
			direct, err := ipns.NewRecord(sk, path.FromCid(c), seq, validity, ipnsTTL, ipns.WithV1Compatibility(false))
			if err != nil {
				t.Fatal(err)
			}
			directBytes, _ := ipns.MarshalRecord(direct)
			if !bytes.Equal(completed, directBytes) {
				t.Fatal("external signature differs from Boxo signing")
			}
		})
	}
}

func TestMobileRecordRejectsInvalidPreparation(t *testing.T) {
	_, name := mobileTestKey(t, 0)
	_, secpPublic, err := crypto.GenerateSecp256k1Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	secpID, _ := peer.IDFromPublicKey(secpPublic)
	for _, tc := range []struct {
		name, ipns, cid string
		sequence        uint64
	}{
		{"invalid name", "invalid", mobileFixtureCID, 1},
		{"non Ed25519 key", ipns.NameFromPeer(secpID).String(), mobileFixtureCID, 1},
		{"invalid CID", name, "invalid", 1},
		{"sequence overflow", name, mobileFixtureCID, math.MaxInt64 + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := NewUnsignedRecord(tc.ipns, tc.cid, tc.sequence); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}

func TestMobileRecordRejectsSignatureSubstitution(t *testing.T) {
	private, name := mobileTestKey(t, 0)
	otherPrivate, otherName := mobileTestKey(t, 1)
	record, payload, err := NewUnsignedRecord(name, mobileFixtureCID, 42)
	if err != nil {
		t.Fatal(err)
	}
	changedSequence, _, _ := NewUnsignedRecord(name, mobileFixtureCID, 43)
	changedCID, _, _ := NewUnsignedRecord(name, "bafkqaaa", 42)
	signature := ed25519.Sign(private, payload)
	for _, tc := range []struct {
		title, name       string
		record, signature []byte
	}{
		{"different key", otherName, record, signature},
		{"wrong signature", name, record, ed25519.Sign(otherPrivate, payload)},
		{"short signature", name, record, signature[:63]},
		{"different sequence", name, changedSequence, signature},
		{"different CID", name, changedCID, signature},
		{"malformed record", name, []byte("invalid"), signature},
		{"oversized record", name, make([]byte, ipns.MaxRecordSize+1), signature},
	} {
		t.Run(tc.title, func(t *testing.T) {
			if _, err := CompleteRecord(tc.name, tc.record, tc.signature); err == nil {
				t.Fatal("accepted invalid signature or substituted content")
			}
		})
	}
}

func TestMobileRecordRejectsUnsupportedSignedRecords(t *testing.T) {
	private, name := mobileTestKey(t, 0)
	public, _ := crypto.UnmarshalEd25519PublicKey(private.Public().(ed25519.PublicKey))
	root, _ := path.NewPath("/ipfs/" + mobileFixtureCID)
	subpath, _ := path.NewPath("/ipfs/" + mobileFixtureCID + "/post")
	nestedName, _ := path.NewPath("/ipns/" + name)
	for _, tc := range []struct {
		title    string
		value    path.Path
		sequence uint64
		ttl      time.Duration
		expired  bool
		options  []ipns.Option
	}{
		{"IPNS target", nestedName, 1, ipnsTTL, false, nil},
		{"subpath", subpath, 1, ipnsTTL, false, nil},
		{"TTL change", root, 1, time.Hour, false, nil},
		{"negative sequence", root, math.MaxUint64, ipnsTTL, false, nil},
		{"expired", root, 1, ipnsTTL, true, nil},
		{"extra metadata", root, 1, ipnsTTL, false, []ipns.Option{ipns.WithMetadata(map[string]any{"_extra": "not allowed"})}},
		{"embedded public key", root, 1, ipnsTTL, false, []ipns.Option{ipns.WithPublicKey(true)}},
	} {
		t.Run(tc.title, func(t *testing.T) {
			validity := time.Now().Add(time.Hour)
			if tc.expired {
				validity = time.Now().Add(-time.Hour)
			}
			capture := &recordSigningCapture{public: public}
			options := append([]ipns.Option{ipns.WithV1Compatibility(false)}, tc.options...)
			record, err := ipns.NewRecord(capture, tc.value, tc.sequence, validity, tc.ttl, options...)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := ipns.MarshalRecord(record)
			if _, err := CompleteRecord(name, raw, ed25519.Sign(private, capture.payload)); err == nil {
				t.Fatal("accepted unsupported record despite a valid signature")
			}
		})
	}
	// The proposal is specifically an unsigned record, not an arbitrary signed
	// envelope whose signature the client asks us to replace.
	record, payload, _ := NewUnsignedRecord(name, mobileFixtureCID, 1)
	signature := ed25519.Sign(private, payload)
	completed, err := CompleteRecord(name, record, signature)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompleteRecord(name, completed, signature); err == nil {
		t.Fatal("accepted a signed envelope as an unsigned proposal")
	}
}

// TestMobileProtocolFixture regenerates the cross-client fixture using Boxo
// and the standard library. Its key comes from the public sequence 00..1f and
// must never be used for a real site. Keeping expected output as a checked-in
// artifact makes JS, Swift, and Kotlin prove interoperation with the publisher.
func TestMobileProtocolFixture(t *testing.T) {
	private, name := mobileTestKey(t, 0)
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	const fixtureTime int64 = 4070908800 // 2099-01-01, frozen client clock for the fixture.
	validity := time.Unix(fixtureTime, 0).UTC().Add(ipnsLifetime)
	record, payload, err := newUnsignedRecordAt(name, mobileFixtureCID, 42, validity)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(private, payload)
	completed, err := CompleteRecord(name, record, signature)
	if err != nil {
		t.Fatal(err)
	}
	var envelope ipnspb.IpnsRecord
	if err := proto.Unmarshal(record, &envelope); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, append([]byte("ipns-signature:"), envelope.Data...)) {
		t.Fatal("Boxo IPNS signature payload changed")
	}
	push := fmt.Sprintf("croptop-push\ncrop.top\n%s\n%s\n42\n%d", name, mobileFixtureCID, fixtureTime)
	challenge := fmt.Sprintf("croptop-mobile-session\nhttps://crop.top\n%s\nmobile-fixture-challenge-00000001\n%d", name, fixtureTime+600)
	enc := base64.StdEncoding.EncodeToString
	fixture := map[string]any{
		"version":         1,
		"warning":         "TEST ONLY. Public deterministic Ed25519 seed 000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f. Never import this key into a real site or publish this record.",
		"privateKeyPEM":   string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"publicKey":       enc(private.Public().(ed25519.PublicKey)),
		"ipns":            name,
		"cid":             mobileFixtureCID,
		"sequence":        "42",
		"host":            "crop.top",
		"origin":          "https://crop.top",
		"time":            fixtureTime,
		"expiresAt":       fixtureTime + 540,
		"recordValidity":  validity.Format(time.RFC3339Nano),
		"recordTTLNanos":  strconv.FormatInt(int64(ipnsTTL), 10),
		"recordPayload":   enc(payload),
		"recordSignature": enc(signature),
		"unsignedRecord":  enc(record),
		"record":          enc(completed),
		"pushPayload":     enc([]byte(push)),
		"pushSignature":   enc(ed25519.Sign(private, []byte(push))),
		"sessionChallenge": map[string]any{
			"id": "mobile-fixture-challenge-00000001", "message": challenge,
			"expiresAt": fixtureTime + 600, "signature": enc(ed25519.Sign(private, []byte(challenge))),
		},
		"recordValidation": "Require prefix ipns-signature: followed by exactly one canonical DAG-CBOR map with exactly five fields, in order TTL, Value, Sequence, Validity, ValidityType. TTL is integer 60000000000 (nanoseconds); Value is bytes equal to UTF-8 /ipfs/<proposal.cid>; Sequence is a nonnegative integer <= 9223372036854775807 exactly equal to the decimal proposal.sequence; Validity is bytes containing an RFC3339Nano UTC timestamp later than the proposal expiry and no later than proposal.time + 7200 hours + 60 seconds; ValidityType is integer 0. Reject duplicate, extra, reordered, incorrectly typed, non-canonical, truncated, or trailing values. Fixture tests freeze the current time to the fixture time field.",
	}
	generated, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	generated = append(generated, '\n')
	stored, err := os.ReadFile("../../docs/design/mobile-protocol-fixture.json")
	if err != nil || !bytes.Equal(generated, stored) {
		t.Fatalf("protocol fixture mismatch (%v); expected:\n%s", err, generated)
	}
}

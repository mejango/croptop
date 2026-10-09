package ipfs

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/ipfs/boxo/ipns"
	ipnspb "github.com/ipfs/boxo/ipns/pb"
	"github.com/ipfs/boxo/path"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/crypto"
	cryptopb "github.com/libp2p/go-libp2p/core/crypto/pb"
	"google.golang.org/protobuf/proto"
)

// NewUnsignedRecord prepares an IPNS v2 record without possessing the site's
// private key. The returned payload must be validated and Ed25519-signed by the
// key holder. Keep record with the proposal and pass it to CompleteRecord;
// neither return value is a publishable IPNS record yet.
func NewUnsignedRecord(ipnsName, c string, sequence uint64) (record, payload []byte, err error) {
	return newUnsignedRecordAt(ipnsName, c, sequence, time.Now().Add(ipnsLifetime))
}

func newUnsignedRecordAt(ipnsName, c string, sequence uint64, validity time.Time) ([]byte, []byte, error) {
	_, public, err := mobileRecordKey(ipnsName)
	if err != nil {
		return nil, nil, err
	}
	id, err := cid.Decode(c)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid mobile record CID: %w", err)
	}
	// Boxo represents DAG-CBOR integers as int64. Reject overflow instead of
	// emitting a negative sequence a client could interpret differently.
	if sequence > math.MaxInt64 {
		return nil, nil, errors.New("mobile record sequence exceeds signed 64-bit range")
	}
	capture := &recordSigningCapture{public: public}
	rec, err := ipns.NewRecord(capture, path.FromCid(id), sequence, validity, ipnsTTL, ipns.WithV1Compatibility(false))
	if err != nil {
		return nil, nil, fmt.Errorf("prepare mobile IPNS record: %w", err)
	}
	b, err := ipns.MarshalRecord(rec)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal mobile IPNS record: %w", err)
	}
	return b, capture.payload, nil
}

// CompleteRecord attaches an external signature to a previously prepared
// record, then independently verifies it with Boxo against the IPNS identity.
// record must be the service's saved proposal, never a replacement supplied by
// the client. The caller must also match its CID and sequence to the prepared
// post before publishing.
func CompleteRecord(ipnsName string, record, signature []byte) ([]byte, error) {
	name, _, err := mobileRecordKey(ipnsName)
	if err != nil {
		return nil, err
	}
	if len(signature) != ed25519.SignatureSize {
		return nil, errors.New("mobile record requires a 64-byte Ed25519 signature")
	}
	rec, err := ipns.UnmarshalRecord(record)
	if err != nil {
		return nil, fmt.Errorf("invalid unsigned mobile record: %w", err)
	}
	value, err := rec.Value()
	if err != nil || value.Namespace() != path.IPFSNamespace {
		return nil, errors.New("mobile record must point to an IPFS root")
	}
	id, err := cid.Decode(value.Segments()[1])
	if err != nil || value.String() != path.FromCid(id).String() {
		return nil, errors.New("mobile record must point to an IPFS root")
	}
	sequence, err := rec.Sequence()
	if err != nil {
		return nil, fmt.Errorf("invalid mobile record sequence: %w", err)
	}
	validity, err := rec.Validity()
	if err != nil {
		return nil, fmt.Errorf("invalid mobile record validity: %w", err)
	}
	// Reconstruct through the same library to reject extra fields, alternate
	// encodings, invalid integer ranges, and changes to the fixed TTL. This
	// also keeps protocol encoding rules owned by Boxo, not this adapter.
	expected, _, err := newUnsignedRecordAt(ipnsName, id.String(), sequence, validity)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(expected, record) {
		return nil, errors.New("mobile record does not match the unsigned v2 format")
	}
	var envelope ipnspb.IpnsRecord
	if err := proto.Unmarshal(record, &envelope); err != nil {
		return nil, fmt.Errorf("decode mobile record envelope: %w", err)
	}
	envelope.SignatureV2 = bytes.Clone(signature)
	completed, err := proto.Marshal(&envelope)
	if err != nil {
		return nil, fmt.Errorf("encode completed mobile record: %w", err)
	}
	verified, err := ipns.UnmarshalRecord(completed)
	if err != nil {
		return nil, fmt.Errorf("decode completed mobile record: %w", err)
	}
	if err := ipns.ValidateWithName(verified, name); err != nil {
		return nil, fmt.Errorf("verify completed mobile record: %w", err)
	}
	return completed, nil
}

func mobileRecordKey(ipnsName string) (ipns.Name, crypto.PubKey, error) {
	name, err := ipns.NameFromString(ipnsName)
	if err != nil {
		return ipns.Name{}, nil, fmt.Errorf("invalid mobile record IPNS name: %w", err)
	}
	public, err := name.Peer().ExtractPublicKey()
	if err != nil {
		return ipns.Name{}, nil, fmt.Errorf("mobile IPNS name must contain its Ed25519 public key: %w", err)
	}
	if public.Type() != cryptopb.KeyType_Ed25519 {
		return ipns.Name{}, nil, errors.New("mobile publishing requires an Ed25519 site key")
	}
	return name, public, nil
}

// recordSigningCapture implements the library's signing interface with only a
// public key. It captures exactly the bytes Boxo asks the device to sign.
type recordSigningCapture struct {
	public  crypto.PubKey
	payload []byte
}

func (c *recordSigningCapture) Type() cryptopb.KeyType   { return c.public.Type() }
func (c *recordSigningCapture) GetPublic() crypto.PubKey { return c.public }
func (c *recordSigningCapture) Raw() ([]byte, error) {
	return nil, errors.New("external signer has no private key")
}
func (c *recordSigningCapture) Equals(other crypto.Key) bool {
	o, ok := other.(*recordSigningCapture)
	return ok && c.public.Equals(o.public)
}
func (c *recordSigningCapture) Sign(payload []byte) ([]byte, error) {
	if c.payload != nil {
		return nil, errors.New("external signer only supports one IPNS v2 payload")
	}
	c.payload = bytes.Clone(payload)
	return nil, nil
}

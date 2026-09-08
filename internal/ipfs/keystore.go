package ipfs

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base32"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
)

// Keystore reads and writes kubo's on-disk keystore directly, so keys can be
// moved without the daemon and while another kubo (the Mac app's) holds its
// repo lock. Kubo stores each key as a libp2p protobuf PrivateKey:
//
//	08 01        field 1 varint  Type = Ed25519
//	12 40 <64B>  field 2 bytes   seed || public key
//
// named "key_" + lowercase unpadded base32 of the key name.
type Keystore struct {
	Dir string
}

func KeystoreFilename(name string) string {
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	return "key_" + strings.ToLower(enc.EncodeToString([]byte(name)))
}

func (k *Keystore) path(name string) string { return filepath.Join(k.Dir, KeystoreFilename(name)) }

func (k *Keystore) Has(name string) bool {
	_, err := os.Stat(k.path(name))
	return err == nil
}

func (k *Keystore) private(name string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(k.path(name))
	if err != nil {
		return nil, err
	}
	return parsePrivProto(b)
}

func (k *Keystore) write(name string, priv ed25519.PrivateKey) error {
	if err := os.MkdirAll(k.Dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(k.path(name), marshalPrivProto(priv), 0o600)
}

// ImportRaw copies a kubo keystore file's bytes under name.
func (k *Keystore) ImportRaw(name string, protobuf []byte) error {
	priv, err := parsePrivProto(protobuf)
	if err != nil {
		return err
	}
	return k.write(name, priv)
}

func (k *Keystore) ImportPEM(name string, pemBytes []byte) error {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return errors.New("not a PEM file")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse PKCS8: %w", err)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return errors.New("IPNS keys must be ed25519")
	}
	return k.write(name, priv)
}

func (k *Keystore) ExportPEM(name string) ([]byte, error) {
	priv, err := k.private(name)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// Name returns the IPNS name (k51...) for the key.
func (k *Keystore) Name(name string) (string, error) {
	priv, err := k.private(name)
	if err != nil {
		return "", err
	}
	return ipnsName(priv.Public().(ed25519.PublicKey)), nil
}

func (k *Keystore) Generate(name string) (string, error) {
	if k.Has(name) {
		return "", fmt.Errorf("key %s already exists", name)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	if err := k.write(name, priv); err != nil {
		return "", err
	}
	return ipnsName(pub), nil
}

func (k *Keystore) Delete(name string) error { return os.Remove(k.path(name)) }

func marshalPrivProto(priv ed25519.PrivateKey) []byte {
	return append([]byte{0x08, 0x01, 0x12, 0x40}, priv...)
}

func parsePrivProto(b []byte) (ed25519.PrivateKey, error) {
	if len(b) != 68 || b[0] != 0x08 || b[1] != 0x01 || b[2] != 0x12 || b[3] != 0x40 {
		return nil, errors.New("not an ed25519 kubo key file")
	}
	return ed25519.PrivateKey(append([]byte(nil), b[4:]...)), nil
}

// ipnsName encodes the public key the way kubo names IPNS keys: protobuf
// PublicKey -> identity multihash -> CIDv1 libp2p-key -> base36 "k...".
func ipnsName(pub ed25519.PublicKey) string {
	pubProto := append([]byte{0x08, 0x01, 0x12, 0x20}, pub...)
	mh := append([]byte{0x00, byte(len(pubProto))}, pubProto...)
	cid := append([]byte{0x01, 0x72}, mh...)
	return "k" + new(big.Int).SetBytes(cid).Text(36)
}

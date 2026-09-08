package ipfs

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestKeystoreFilename(t *testing.T) {
	got := KeystoreFilename("7B0816A5-9162-421E-AA21-88A5097CBA99")
	want := "key_g5bdaobrgzatkljzge3deljugiyuklkbiezdcljyhbatkmbzg5bueqjzhe"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestPEMRoundTripAndName(t *testing.T) {
	ks := &Keystore{Dir: filepath.Join(t.TempDir(), "keystore")}
	_, priv, _ := ed25519.GenerateKey(nil)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	p := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := ks.ImportPEM("site", p); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(ks.Dir, KeystoreFilename("site")))
	if len(raw) != 68 || raw[0] != 0x08 || raw[1] != 0x01 || raw[2] != 0x12 || raw[3] != 0x40 {
		t.Fatalf("bad protobuf: %x", raw[:4])
	}
	out, err := ks.ExportPEM("site")
	if err != nil || string(out) != string(p) {
		t.Fatalf("export mismatch: %v\n%s\n%s", err, out, p)
	}
	name, err := ks.Name("site")
	if err != nil || len(name) != 62 || name[:3] != "k51" {
		t.Fatalf("bad name %q %v", name, err)
	}
	if !ks.Has("site") || ks.Has("other") {
		t.Fatal("Has wrong")
	}
	gen, err := ks.Generate("other")
	if err != nil || gen[:3] != "k51" {
		t.Fatalf("generate: %q %v", gen, err)
	}
	if _, err := ks.Generate("other"); err == nil {
		t.Fatal("generate must not overwrite")
	}
}

// TestNameMatchesKubo checks name derivation against a key the Mac app made,
// when that library is present on this machine. Skipped elsewhere.
func TestNameMatchesKubo(t *testing.T) {
	home, _ := os.UserHomeDir()
	ks := &Keystore{Dir: filepath.Join(home, "Library/Containers/xyz.planetable.Lite/Data/Library/Application Support/ipfs/keystore")}
	const id = "FF5F456D-904F-4EE6-8BB5-AD175C65319A"
	if !ks.Has(id) {
		t.Skip("no local Croptop library")
	}
	name, err := ks.Name(id)
	if err != nil {
		t.Fatal(err)
	}
	if name != "k51qzi5uqu5dhxiwvl4xx3sco13y50yoo28rbhe3sneirnj2qatinx75qpsqtb" {
		t.Fatalf("derived %s", name)
	}
}

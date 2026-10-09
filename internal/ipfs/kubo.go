// Package ipfs downloads a pinned kubo, runs it as a child process, and
// wraps the CLI calls this program needs.
package ipfs

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const KuboVersion = "v0.43.0"
const distBase = "https://dist.ipfs.tech/kubo/"

func assetName(goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return "kubo_" + KuboVersion + "_" + goos + "-" + goarch + ext
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "ipfs.exe"
	}
	return "ipfs"
}

// EnsureKubo returns the path to a kubo binary of KuboVersion under dir,
// downloading and verifying it when absent. progress receives status lines.
func EnsureKubo(dir string, progress func(string)) (string, error) {
	if progress == nil {
		progress = func(string) {}
	}
	bin := filepath.Join(dir, binaryName())
	if v, err := os.ReadFile(filepath.Join(dir, "VERSION")); err == nil && strings.TrimSpace(string(v)) == KuboVersion {
		if _, err := os.Stat(bin); err == nil {
			return bin, nil
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	asset := assetName(runtime.GOOS, runtime.GOARCH)
	url := distBase + KuboVersion + "/" + asset
	progress("downloading " + url)
	archive, err := fetch(url)
	if err != nil {
		return "", err
	}
	sums, err := fetch(url + ".sha512")
	if err != nil {
		return "", err
	}
	want := strings.Fields(string(sums))[0]
	got := hex.EncodeToString(func() []byte { h := sha512.Sum512(archive); return h[:] }())
	if got != want {
		return "", fmt.Errorf("sha512 mismatch for %s: got %s want %s", url, got, want)
	}
	progress("verified sha512, extracting")
	var body []byte
	if strings.HasSuffix(asset, ".zip") {
		body, err = extractZip(archive, "kubo/"+binaryName())
	} else {
		body, err = extractTarGz(archive, "kubo/"+binaryName())
	}
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(bin, body, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte(KuboVersion+"\n"), 0o644); err != nil {
		return "", err
	}
	progress("kubo " + KuboVersion + " ready")
	return bin, nil
}

func fetch(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func extractTarGz(archive []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Name == name || h.Name == "./"+name {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%s not in archive", name)
}

func extractZip(archive []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%s not in archive", name)
}

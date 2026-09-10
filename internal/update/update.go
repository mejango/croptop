// Package update checks GitHub releases and replaces the running binary.
package update

import (
	"errors"
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const Repo = "mejango/croptop"

type Release struct {
	Tag    string  `json:"tag_name"`
	Assets []Asset `json:"assets"`
}

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Version is a tag without its "v".
func (r Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

// Latest asks GitHub for the newest release.
func Latest(ctx context.Context) (*Release, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "croptop")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github: %s", resp.Status)
	}
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Newer reports whether candidate is a higher version than current.
// Development builds ("dev") never update themselves.
func Newer(current, candidate string) bool {
	if current == "dev" || current == "" {
		return false
	}
	a, b := parts(current), parts(candidate)
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return b[i] > a[i]
		}
	}
	return false
}

func parts(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

// Apply downloads the release for this platform, checks its sha256 against
// errAppBundle is returned when the running binary lives inside a macOS .app;
// replacing it would break the app's code signature, so the whole app updates instead.
var errAppBundle = errors.New("the Croptop app updates itself; download the newest version from https://crop.top")

// InsideAppBundle reports whether exe lives inside a macOS application bundle.
func InsideAppBundle(exe string) bool {
	return strings.Contains(exe, ".app/Contents/")
}

// checksums.txt, and swaps it in for the running executable. It returns the
// executable path; the caller restarts.
func Apply(ctx context.Context, r *Release, log func(string)) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	if InsideAppBundle(exe) {
		return "", errAppBundle
	}
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	want := fmt.Sprintf("croptop_%s_%s_%s%s", r.Version(), runtime.GOOS, runtime.GOARCH, ext)
	var asset, sums *Asset
	for i := range r.Assets {
		switch r.Assets[i].Name {
		case want:
			asset = &r.Assets[i]
		case "checksums.txt":
			sums = &r.Assets[i]
		}
	}
	if asset == nil {
		return "", fmt.Errorf("release %s has no build for %s/%s", r.Tag, runtime.GOOS, runtime.GOARCH)
	}
	log("downloading " + asset.Name)
	archive, err := fetch(ctx, asset.URL)
	if err != nil {
		return "", err
	}
	if sums != nil {
		list, err := fetch(ctx, sums.URL)
		if err != nil {
			return "", err
		}
		sum := hex.EncodeToString(func() []byte { h := sha256.Sum256(archive); return h[:] }())
		if !strings.Contains(string(list), sum+"  "+asset.Name) {
			return "", fmt.Errorf("checksum of %s does not match the release", asset.Name)
		}
	}
	bin, err := extract(archive, ext)
	if err != nil {
		return "", err
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		// a running exe cannot be overwritten, but it can be renamed
		old := exe + ".old"
		os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			os.Remove(tmp)
			return "", err
		}
	}
	if err := os.Rename(tmp, exe); err != nil {
		return "", err
	}
	log("installed croptop " + r.Version())
	return exe, nil
}

// Restart replaces the current process with exe, keeping the arguments.
func Restart(exe string) error {
	if runtime.GOOS == "windows" {
		cmd := exec.Command(exe, os.Args[1:]...)
		cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
		if err := cmd.Start(); err != nil {
			return err
		}
		os.Exit(0)
	}
	return execSelf(exe, os.Args, os.Environ())
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "croptop")
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

// extract pulls the croptop binary out of a release archive.
func extract(archive []byte, ext string) ([]byte, error) {
	if ext == ".zip" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == "croptop.exe" {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("croptop.exe not in archive")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("croptop not in archive")
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) == "croptop" && h.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
}

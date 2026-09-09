package publish

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/store"
)

// Site JSON keys for the host a site pushes to and the free name it claimed.
const (
	HostKey = "croptopHost"
	NameKey = "croptopName"
)

// DefaultHost is where sites push unless they say otherwise.
const DefaultHost = "https://crop.top"

func rawString(site *store.Site, key string) string {
	if raw, ok := site.Raw[key]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func setRaw(site *store.Site, key, val string) {
	if site.Raw == nil {
		site.Raw = map[string]json.RawMessage{}
	}
	b, _ := json.Marshal(val)
	site.Raw[key] = b
}

// HostOf is the host base URL a site pushes to, "" when pushing is off.
func HostOf(site *store.Site) string { return strings.TrimSuffix(rawString(site, HostKey), "/") }

// NameOf is the free name the site claimed on its host, or "".
func NameOf(site *store.Site) string { return rawString(site, NameKey) }

// SetHost turns pushing on (a base URL) or off ("").
func SetHost(site *store.Site, base string) {
	setRaw(site, HostKey, strings.TrimSuffix(strings.TrimSpace(base), "/"))
}

func hostDomain(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("host must be a URL like https://crop.top")
	}
	return strings.ToLower(u.Hostname()), nil
}

// Push uploads the published site's files to its host so the host serves
// them at once and keeps the IPNS record alive. The host checks that the
// files hash to cid before it believes the sequence.
func (p *Publisher) Push(ctx context.Context, site *store.Site, cid string, seq uint64) error {
	base := HostOf(site)
	if base == "" {
		return nil
	}
	domain, err := hostDomain(base)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	sig, err := p.Node.Keystore().Sign(site.ID, host.PushMessage(domain, site.IPNS, cid, seq, now))
	if err != nil {
		return err
	}
	dir := p.Store.PublicDir(site.ID)
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(dir, path)
			part, err := mw.CreateFormFile("file:"+filepath.ToSlash(rel), filepath.Base(rel))
			if err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(part, f)
			return err
		})
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/v0/host/push", pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Croptop-Ipns", site.IPNS)
	req.Header.Set("X-Croptop-Cid", cid)
	req.Header.Set("X-Croptop-Seq", strconv.FormatUint(seq, 10))
	req.Header.Set("X-Croptop-Time", strconv.FormatInt(now, 10))
	req.Header.Set("X-Croptop-Sig", base64.StdEncoding.EncodeToString(sig))
	if rs, ok := p.Node.(interface{ Record(string) []byte }); ok {
		if rec := rs.Record(site.ID); len(rec) > 0 {
			req.Header.Set("X-Croptop-Record", base64.StdEncoding.EncodeToString(rec))
		}
	}
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// Claim asks the site's host for a free name and records it on success.
func (p *Publisher) Claim(ctx context.Context, site *store.Site, name string) error {
	base := HostOf(site)
	if base == "" {
		base = DefaultHost
		SetHost(site, base)
	}
	domain, err := hostDomain(base)
	if err != nil {
		return err
	}
	name = strings.ToLower(strings.TrimSpace(name))
	now := time.Now().Unix()
	sig, err := p.Node.Keystore().Sign(site.ID, host.ClaimMessage(domain, name, site.IPNS, now))
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"name": name, "ipns": site.IPNS, "time": now, "sig": base64.StdEncoding.EncodeToString(sig)})
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/v0/host/names", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s", strings.TrimSpace(string(msg)))
	}
	setRaw(site, NameKey, name)
	return p.Store.SaveSite(site)
}

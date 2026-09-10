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

// Hosts behind Cloudflare accept at most 100 MB per request, so a site goes
// up in batches, and a single file bigger than a batch goes in chunks that
// the host reassembles.
const (
	pushBatch = 16 << 20
	pushChunk = 64 << 20
)

// Push uploads the published site's files to its host so the host serves
// them at once and keeps the IPNS record alive. Files go in batches under
// the request size hosts accept; each batch carries the same signature and
// the last one is marked final. The host checks that the files hash to cid.
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
	var record string
	if rs, ok := p.Node.(interface{ Record(string) []byte }); ok {
		if rec := rs.Record(site.ID); len(rec) > 0 {
			record = base64.StdEncoding.EncodeToString(rec)
		}
	}
	dir := p.Store.PublicDir(site.ID)
	type file struct {
		rel  string
		size int64
	}
	var files []file
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		files = append(files, file{filepath.ToSlash(rel), info.Size()})
		return nil
	})
	if err != nil {
		return err
	}
	signed := func(req *http.Request) {
		req.Header.Set("X-Croptop-Ipns", site.IPNS)
		req.Header.Set("X-Croptop-Cid", cid)
		req.Header.Set("X-Croptop-Seq", strconv.FormatUint(seq, 10))
		req.Header.Set("X-Croptop-Time", strconv.FormatInt(now, 10))
		req.Header.Set("X-Croptop-Sig", base64.StdEncoding.EncodeToString(sig))
		if record != "" {
			req.Header.Set("X-Croptop-Record", record)
		}
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	// big files first, in chunks; the host reassembles them
	var small []file
	for _, f := range files {
		if f.size <= pushBatch {
			small = append(small, f)
			continue
		}
		chunks := int((f.size + pushChunk - 1) / pushChunk)
		upload := ""
		fh, err := os.Open(filepath.Join(dir, filepath.FromSlash(f.rel)))
		if err != nil {
			return err
		}
		for i := 0; i < chunks; i++ {
			size := int64(pushChunk)
			if rest := f.size - int64(i)*pushChunk; rest < size {
				size = rest
			}
			req, err := http.NewRequestWithContext(ctx, "POST", base+"/v0/host/push", io.NewSectionReader(fh, int64(i)*pushChunk, size))
			if err != nil {
				fh.Close()
				return err
			}
			req.ContentLength = size
			req.Header.Set("Content-Type", "application/octet-stream")
			signed(req)
			req.Header.Set("X-Croptop-File", f.rel)
			req.Header.Set("X-Croptop-Chunk", fmt.Sprintf("%d/%d", i+1, chunks))
			if upload != "" {
				req.Header.Set("X-Croptop-Upload", upload)
			}
			resp, err := client.Do(req)
			if err != nil {
				fh.Close()
				return err
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode != 200 {
				fh.Close()
				return fmt.Errorf("%s chunk %d of %d: %s: %s", f.rel, i+1, chunks, resp.Status, strings.TrimSpace(string(body)))
			}
			var out struct{ Upload string }
			json.Unmarshal(body, &out)
			if out.Upload != "" {
				upload = out.Upload
			}
		}
		fh.Close()
		p.log("pushed %s in %d chunks", f.rel, chunks)
	}
	files = small
	var batches [][]file
	var cur []file
	var curSize int64
	for _, f := range files {
		if len(cur) > 0 && curSize+f.size > pushBatch {
			batches = append(batches, cur)
			cur, curSize = nil, 0
		}
		cur = append(cur, f)
		curSize += f.size
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	for i, batch := range batches {
		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		go func() {
			var err error
			for _, f := range batch {
				part, e := mw.CreateFormFile("file:"+f.rel, filepath.Base(f.rel))
				if e != nil {
					err = e
					break
				}
				fh, e := os.Open(filepath.Join(dir, filepath.FromSlash(f.rel)))
				if e != nil {
					err = e
					break
				}
				_, e = io.Copy(part, fh)
				fh.Close()
				if e != nil {
					err = e
					break
				}
			}
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
		signed(req)
		req.Header.Set("X-Croptop-Part", fmt.Sprintf("%d/%d", i+1, len(batches)))
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("part %d of %d: %s: %s", i+1, len(batches), resp.Status, strings.TrimSpace(string(body)))
		}
	}
	if len(batches) > 1 {
		p.log("pushed %s in %d parts", site.Name, len(batches))
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

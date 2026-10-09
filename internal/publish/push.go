package publish

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"sync"
	"time"

	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/store"
)

// Site JSON keys for the host a site pushes to and the free name it claimed.
const (
	HostKey = "croptopHost"
	NameKey = "croptopName"
)

// DefaultHost is the endpoint for hosted sites that choose no custom host.
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

// HostOf resolves an endpoint independently of the storage policy. It does
// not imply that a new or existing site has opted into reliable hosting.
func HostOf(site *store.Site) string {
	base := strings.TrimSuffix(rawString(site, HostKey), "/")
	if base == "" {
		return DefaultHost
	}
	return base
}

// NameOf is the free name the site claimed on its host, or "".
func NameOf(site *store.Site) string { return rawString(site, NameKey) }

// SetHost chooses a custom host. An empty value restores DefaultHost.
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

// Hosts behind Cloudflare accept at most 100 MB per request, so a site goes up
// in batches, and a single file bigger than a batch goes in chunks that the
// host reassembles. A part of a push holds files up to pushBatch bytes; bigger
// files go alone in pushChunk pieces. Variables so tests can make sites split.
var pushBatch, pushChunk int64 = 16 << 20, 64 << 20

// stallAfter is how long an upload may send nothing before it is given up;
// replyWithin bounds the wait for the host's answer once all of it is sent.
// A push has no deadline otherwise, so a slow link still finishes.
var stallAfter, replyWithin = 2 * time.Minute, 5 * time.Minute

// pushClock is the time each push request is signed with. Hosts refuse a
// signature over 10 minutes old, so every request of a long push is signed
// when it goes, not when the push began. A variable so tests can move it.
var pushClock = time.Now

var (
	errStalled  = errors.New("upload stalled")
	errNoAnswer = errors.New("host gave no answer")
	// errNoCommitFile: the part that commits a version must carry a file
	// small enough for a part, and this push has none.
	errNoCommitFile = errors.New("nothing small enough to carry the commit; send the whole site")
)

// pushSpec says what a push sends. With no Dir the files come from the
// version's own blocks, so an upload sends exactly that version even if the
// site is rendered again while it runs.
type pushSpec struct {
	Parent string   // the version the host must still hold for the push to apply
	Files  []string // the version's files to send; nil sends them all
	Carry  []string // non-nil makes a manifest push: the version is Files plus these paths of Parent
	Dir    string   // send the files under this directory instead (post --key's staging dir)
	Record []byte   // the signed record the host is to serve; nil takes the one the engine last signed for the key
}

// hostConflict is a push the host refused because the site moved on (409).
type hostConflict struct{ msg string }

func (e *hostConflict) Error() string { return e.msg }

// hostRefusal is any other answer than 200 or 409: the host took the request
// and said no, which sending the same again will not change.
type hostRefusal struct{ msg string }

func (e *hostRefusal) Error() string { return e.msg }

// versionReader reads a version's files from the engine's blocks: the
// embedded engine does, the kubo one does not.
type versionReader interface {
	Files(ctx context.Context, root string) ([]ipfs.VersionFile, error)
	OpenFile(ctx context.Context, root, rel string) (io.ReadSeekCloser, int64, error)
}

type pushFile struct {
	rel  string
	size int64
}

// pushFiles lists what a push sends, with sizes, and how to open each file.
func (p *Publisher) pushFiles(ctx context.Context, cid string, spec pushSpec) ([]pushFile, func(string) (io.ReadCloser, error), error) {
	want := map[string]bool{}
	for _, f := range spec.Files {
		want[f] = true
	}
	var files []pushFile
	if spec.Dir == "" {
		vr, ok := p.Node.(versionReader)
		if !ok {
			return nil, nil, fmt.Errorf("this engine cannot read a version's files; push from a folder")
		}
		all, err := vr.Files(ctx, cid)
		if err != nil {
			return nil, nil, err
		}
		for _, f := range all {
			if spec.Files == nil || want[f.Path] {
				files = append(files, pushFile{f.Path, f.Size})
			}
		}
		return files, func(rel string) (io.ReadCloser, error) {
			r, _, err := vr.OpenFile(ctx, cid, rel)
			return r, err
		}, nil
	}
	err := filepath.WalkDir(spec.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(spec.Dir, path)
		if rel = filepath.ToSlash(rel); spec.Files == nil || want[rel] {
			files = append(files, pushFile{rel, info.Size()})
		}
		return nil
	})
	return files, func(rel string) (io.ReadCloser, error) {
		return os.Open(filepath.Join(spec.Dir, filepath.FromSlash(rel)))
	}, err
}

// heldFiles asks the host which files of version cid it already holds, from
// an earlier attempt that did not finish. A host that cannot say holds none.
func heldFiles(ctx context.Context, base, cid string) map[string]int64 {
	hctx, cancel := context.WithTimeout(ctx, hostTimeout)
	defer cancel()
	b, status, err := httpGet(hctx, base+"/v0/host/versions/"+cid+"/files")
	if err != nil || status != 200 {
		return nil
	}
	var list []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	if json.Unmarshal(b, &list) != nil {
		return nil
	}
	out := make(map[string]int64, len(list))
	for _, f := range list {
		out[f.Path] = f.Size
	}
	return out
}

// skipHeld drops the files the host already holds at the same size. The final
// part, which commits the version, must carry a file small enough for a part,
// so when none of those is left it sends the smallest such file again.
func skipHeld(files []pushFile, have map[string]int64) []pushFile {
	var out []pushFile
	small := -1 // the smallest file that fits in a part
	for i, f := range files {
		if size, ok := have[f.rel]; !ok || size != f.size {
			out = append(out, f)
		}
		if f.size <= pushBatch && (small < 0 || f.size < files[small].size) {
			small = i
		}
	}
	for _, f := range out {
		if f.size <= pushBatch {
			return out
		}
	}
	if small >= 0 {
		out = append(out, files[small])
	}
	return out
}

// sendWatched sends req and returns its response. It cancels the request if
// the body stops moving for stallAfter (errStalled), or if the host has not
// answered, reading the answer included, replyWithin after the body was sent
// (errNoAnswer). There is no deadline beyond that. A canceled request returns
// at once, even if its body is stuck in Read. Closing the answer ends the
// watch.
func sendWatched(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	// the limits are read once, here: tests change them, and the watchdog
	// runs on a goroutine of its own
	w := &watched{last: time.Now(), stall: stallAfter, reply: replyWithin}
	if req.Body != nil {
		w.r = req.Body
		req.Body = w
	} else {
		w.done = true
	}
	req = req.WithContext(ctx)
	stop := make(chan struct{})
	end := sync.OnceFunc(func() { close(stop); cancel(nil) })
	go func() {
		t := time.NewTicker(w.stall / 10)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if why := w.overdue(); why != nil {
					cancel(why)
					return
				}
			}
		}
	}()
	// The transport waits for whatever is writing the body even after the
	// request is canceled, so a body stuck in Read would keep Do from ever
	// returning. Do runs on its own: a canceled request returns here at once,
	// and the transport finishes when the body lets go.
	type sent struct {
		resp *http.Response
		err  error
	}
	done := make(chan sent, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		done <- sent{resp, err}
	}()
	var s sent
	select {
	case s = <-done:
	case <-ctx.Done():
		go func() { // an answer that comes after this is not wanted
			if late := <-done; late.resp != nil {
				late.resp.Body.Close()
			}
		}()
	}
	if ctx.Err() != nil { // the watchdog gave up, or the caller did
		if s.resp != nil {
			s.resp.Body.Close()
		}
		end()
		return nil, fmt.Errorf("%s %s: %w", req.Method, req.URL, context.Cause(ctx))
	}
	if s.err != nil {
		end()
		return nil, s.err
	}
	s.resp.Body = &cancelBody{s.resp.Body, ctx, end}
	return s.resp, nil
}

// watched is a request body that records when it last moved.
type watched struct {
	r            io.ReadCloser
	stall, reply time.Duration // stallAfter and replyWithin when the request began
	mu           sync.Mutex
	last         time.Time
	done         bool // all of it was sent; now waiting for the answer
}

func (w *watched) Read(b []byte) (int, error) {
	n, err := w.r.Read(b)
	w.mu.Lock()
	if n > 0 || err == io.EOF {
		w.last = time.Now()
	}
	if err == io.EOF {
		w.done = true
	}
	w.mu.Unlock()
	return n, err
}

func (w *watched) Close() error { return w.r.Close() }

// overdue says why the request should be given up, or nil while it may go on.
func (w *watched) overdue() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case w.done && time.Since(w.last) > w.reply:
		return fmt.Errorf("%w in %s", errNoAnswer, w.reply)
	case !w.done && time.Since(w.last) > w.stall:
		return fmt.Errorf("%w for %s", errStalled, w.stall)
	}
	return nil
}

// cancelBody is a watched request's answer. Reading it stays under the reply
// limit, and a read the watchdog cut short says why; closing it ends the
// watch and the request's context.
type cancelBody struct {
	io.ReadCloser
	ctx context.Context
	end func()
}

func (b *cancelBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF && b.ctx.Err() != nil {
		err = context.Cause(b.ctx)
	}
	return n, err
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.end()
	return err
}

// Push sends a whole version to the site's host. With the embedded engine
// the files come from the version's blocks; with kubo, from the rendered
// folder as before.
func (p *Publisher) Push(ctx context.Context, site *store.Site, cid string, seq uint64) error {
	spec := pushSpec{}
	if _, ok := p.Node.(versionReader); !ok {
		spec.Dir = p.Store.PublicDir(site.ID)
	}
	return p.pushDir(ctx, site, site.ID, cid, seq, spec)
}

// pushDir pushes a version's files, signed with the keystore key named key.
// With spec.Parent the host adds them on top of that version, which it must
// still hold; with spec.Carry it keeps only them and the carried paths. The
// version's folder blocks ride along, so the next reader can list it from the
// host before any IPFS peer has it. A push cut short resumes: the files the
// host already holds for this version are not sent again.
func (p *Publisher) pushDir(ctx context.Context, site *store.Site, key, cid string, seq uint64, spec pushSpec) error {
	site, err := p.hostedSite(site)
	if err != nil {
		return err
	}
	base := HostOf(site)
	domain, err := hostDomain(base)
	if err != nil {
		return err
	}
	var record string
	rec := spec.Record
	if rec == nil {
		rec = recordOf(p.Node, key)
	}
	if len(rec) > 0 {
		record = base64.StdEncoding.EncodeToString(rec)
	}
	var blocks map[string][]byte
	if bs, ok := p.Node.(interface {
		DirBlocks(context.Context, string) (map[string][]byte, error)
	}); ok {
		blocks, _ = bs.DirBlocks(ctx, cid)
	}
	files, open, err := p.pushFiles(ctx, cid, spec)
	if err != nil {
		return err
	}
	if err := p.checkHost(site, base); err != nil {
		return err
	}
	if have := heldFiles(ctx, base, cid); len(have) > 0 {
		files = skipHeld(files, have)
	}
	// each request is signed as it goes: hosts refuse a signature over 10
	// minutes old, and a push on a slow link takes longer than that
	sign := func(req *http.Request) error {
		if err := p.checkHost(site, base); err != nil {
			return err
		}
		t := pushClock().Unix()
		sig, err := p.Node.Keystore().Sign(key, host.PushMessage(domain, site.IPNS, cid, seq, t))
		if err != nil {
			return err
		}
		req.Header.Set("X-Croptop-Ipns", site.IPNS)
		req.Header.Set("X-Croptop-Cid", cid)
		req.Header.Set("X-Croptop-Seq", strconv.FormatUint(seq, 10))
		req.Header.Set("X-Croptop-Time", strconv.FormatInt(t, 10))
		req.Header.Set("X-Croptop-Sig", base64.StdEncoding.EncodeToString(sig))
		if record != "" {
			req.Header.Set("X-Croptop-Record", record)
		}
		if spec.Parent != "" {
			req.Header.Set("X-Croptop-Parent", spec.Parent)
		}
		return nil
	}
	answer := func(resp *http.Response, what string) (string, error) {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("%s: reading the answer: %w", what, err)
		}
		switch resp.StatusCode {
		case 200:
			return string(body), nil
		case 409:
			return "", &hostConflict{fmt.Sprintf("%s: %s", base, strings.TrimSpace(string(body)))}
		}
		msg := fmt.Sprintf("%s: %s: %s", what, resp.Status, strings.TrimSpace(string(body)))
		if resp.StatusCode == http.StatusTooManyRequests {
			return "", errors.New(msg) // later, not never
		}
		return "", &hostRefusal{msg}
	}
	var small, big []pushFile
	for _, f := range files {
		if f.size <= pushBatch {
			small = append(small, f)
		} else {
			big = append(big, f)
		}
	}
	if len(small) == 0 {
		return errNoCommitFile // checked before any chunk goes up
	}
	// big files first, in chunks; the host reassembles them
	for _, f := range big {
		chunks := int((f.size + pushChunk - 1) / pushChunk)
		rc, err := open(f.rel)
		if err != nil {
			return err
		}
		upload := ""
		for i := 0; i < chunks; i++ {
			size := pushChunk
			if rest := f.size - int64(i)*pushChunk; rest < size {
				size = rest
			}
			req, err := http.NewRequestWithContext(ctx, "POST", base+"/v0/host/push", io.LimitReader(rc, size))
			if err != nil {
				rc.Close()
				return err
			}
			req.ContentLength = size
			req.Header.Set("Content-Type", "application/octet-stream")
			if err := sign(req); err != nil {
				rc.Close()
				return err
			}
			req.Header.Set("X-Croptop-File", f.rel)
			req.Header.Set("X-Croptop-Chunk", fmt.Sprintf("%d/%d", i+1, chunks))
			if upload != "" {
				req.Header.Set("X-Croptop-Upload", upload)
			}
			resp, err := sendWatched(req)
			if err != nil {
				rc.Close()
				return err
			}
			body, err := answer(resp, fmt.Sprintf("%s chunk %d of %d", f.rel, i+1, chunks))
			if err != nil {
				rc.Close()
				return err
			}
			var out struct{ Upload string }
			json.Unmarshal([]byte(body), &out)
			if out.Upload != "" {
				upload = out.Upload
			}
		}
		rc.Close()
		p.log("pushed %s in %d chunks", f.rel, chunks)
	}
	var batches [][]pushFile
	var cur []pushFile
	var curSize int64
	for _, f := range small {
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
		final := i == len(batches)-1
		go func() {
			var err error
			for _, f := range batch {
				part, e := mw.CreateFormFile("file:"+f.rel, filepath.Base(f.rel))
				if e != nil {
					err = e
					break
				}
				rc, e := open(f.rel)
				if e != nil {
					err = e
					break
				}
				_, e = io.Copy(part, rc)
				rc.Close()
				if e != nil {
					err = e
					break
				}
			}
			if err == nil && final {
				for c, data := range blocks {
					var part io.Writer
					if part, err = mw.CreateFormFile("block:"+c, c); err == nil {
						_, err = part.Write(data)
					}
					if err != nil {
						break
					}
				}
				if err == nil && spec.Carry != nil {
					m, _ := json.Marshal(map[string][]string{"carry": spec.Carry})
					err = mw.WriteField("manifest", string(m))
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
		if err := sign(req); err != nil {
			pr.CloseWithError(err)
			return err
		}
		req.Header.Set("X-Croptop-Part", fmt.Sprintf("%d/%d", i+1, len(batches)))
		resp, err := sendWatched(req)
		if err != nil {
			pr.CloseWithError(err)
			return err
		}
		if _, err := answer(resp, fmt.Sprintf("part %d of %d", i+1, len(batches))); err != nil {
			return err
		}
	}
	if len(batches) > 1 {
		p.log("pushed %s in %d parts", site.Name, len(batches))
	}
	return nil
}

// Claim asks the site's host for a free name and records it on success.
func (p *Publisher) Claim(ctx context.Context, site *store.Site, name string) error {
	requested := site
	site, err := p.hostedSite(site)
	if err != nil {
		return err
	}
	base := HostOf(site)
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
	if err := p.checkHost(site, base); err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s", strings.TrimSpace(string(msg)))
	}
	if err := p.saveSite(site.ID, func(s *store.Site) { setRaw(s, NameKey, name) }); err != nil {
		return err
	}
	setRaw(requested, NameKey, name)
	return nil
}

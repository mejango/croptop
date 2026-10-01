// Package host is the crop.top role: a gateway for ENS, IPNS, and claimed names,
// a pin host that sites push to on publish, and a registry of free names.
package host

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/boxo/gateway"

	"github.com/mejango/croptop/internal/ipfs"
)

const (
	maxPush      = 512 << 20
	maxManifest  = 8 << 20
	skew         = 10 * time.Minute
	resolveCache = time.Minute
)

var errManifestTooLarge = errors.New("manifest too large")

// Entry is what the host knows about one site key.
type Entry struct {
	IPNS     string    `json:"ipns"`
	CID      string    `json:"cid"`
	Sequence uint64    `json:"sequence"`
	Record   []byte    `json:"record,omitempty"` // the site's signed IPNS record
	Name     string    `json:"name,omitempty"`   // claimed free name, if any
	Updated  time.Time `json:"updated"`
}

type registry struct {
	Names map[string]string `json:"names"` // name -> ipns
	Keys  map[string]*Entry `json:"keys"`  // ipns -> entry
}

type Host struct {
	Domain  string
	DataDir string
	Engine  *ipfs.Embedded
	Log     func(string)
	// Root is the site the bare domain serves where no claimed name matches:
	// an ENS name (croptop.eth), an IPNS name, or a CID. Empty shows the directory.
	Root string
	// Trust lists hostnames whose pushes this host accepts when forwarded, as
	// crop.top's Worker replicates every push to its node: the request carries
	// X-Croptop-Signed-Host naming the domain the site signed for.
	Trust []string

	mu    sync.Mutex
	reg   registry
	gw    http.Handler
	cache map[string]cached

	routingSlots   chan struct{} // routing PUTs in flight; full means 429
	lookupSlots    chan struct{} // routing GETs searching the DHT; full means 429
	rebuildTimeout time.Duration // how long a manifest push may rebuild its version
}

type cached struct {
	cid string
	at  time.Time
}

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
var cidRe = regexp.MustCompile(`^(bafy|Qm)[a-zA-Z0-9]+$`)

type stagedFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// stagedFiles lists the files under dir, as the push that put them there
// named them.
func stagedFiles(dir string) []stagedFile {
	out := []stagedFile{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, stagedFile{filepath.ToSlash(rel), info.Size()})
		return nil
	})
	return out
}

// Reserved names the directory and API need, plus the usual suspects.
var reserved = map[string]bool{"www": true, "api": true, "v0": true, "ipfs": true, "ipns": true, "push": true, "host": true, "admin": true, "mail": true, "static": true, "assets": true, "docs": true, "app": true, "routing": true, "agents": true}

func (h *Host) log(f string, a ...any) {
	if h.Log != nil {
		h.Log(fmt.Sprintf(f, a...))
	}
}

func (h *Host) file() string { return filepath.Join(h.DataDir, "host", "registry.json") }

// Start loads the registry and builds the gateway handler. The engine must be running.
func (h *Host) Start() error {
	h.reg = registry{Names: map[string]string{}, Keys: map[string]*Entry{}}
	if b, err := os.ReadFile(h.file()); err == nil {
		if err := json.Unmarshal(b, &h.reg); err != nil {
			return fmt.Errorf("registry: %w", err)
		}
	}
	if h.reg.Names == nil {
		h.reg.Names = map[string]string{}
	}
	if h.reg.Keys == nil {
		h.reg.Keys = map[string]*Entry{}
	}
	backend, err := gateway.NewBlocksBackend(h.Engine.BlockService())
	if err != nil {
		return err
	}
	h.gw = gateway.NewHandler(gateway.Config{DeserializedResponses: true, NoDNSLink: true}, backend)
	h.cache = map[string]cached{}
	// a host is the routing endpoint and a bootstrap peer: it must not send
	// records to routing endpoints (the Worker proxies them back here) or
	// fetch its own peers list
	h.Engine.RoutingPuts, h.Engine.PeersURL = nil, ""
	h.routingSlots = make(chan struct{}, 32)
	h.lookupSlots = make(chan struct{}, 32)
	h.rebuildTimeout = 10 * time.Minute
	// a push left unfinished two days ago will not be resumed
	stage := filepath.Join(h.DataDir, "host", "staging")
	if ents, err := os.ReadDir(stage); err == nil {
		for _, ent := range ents {
			if info, err := ent.Info(); err == nil && time.Since(info.ModTime()) > 48*time.Hour {
				os.RemoveAll(filepath.Join(stage, ent.Name()))
			}
		}
	}
	return nil
}

func (h *Host) save() error {
	if err := os.MkdirAll(filepath.Dir(h.file()), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(h.reg, "", "  ")
	return os.WriteFile(h.file(), b, 0o644)
}

// Run re-announces every pushed record and root until ctx ends: the host is
// the keepalive for sites whose laptops are closed.
func (h *Host) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		h.reannounce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (h *Host) reannounce(ctx context.Context) {
	h.mu.Lock()
	entries := make([]*Entry, 0, len(h.reg.Keys))
	for _, e := range h.reg.Keys {
		entries = append(entries, e)
	}
	h.mu.Unlock()
	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}
		if len(e.Record) > 0 {
			if err := h.Engine.PutRecord(ctx, e.IPNS, e.Record); err != nil {
				h.log("reannounce %s: %v", e.IPNS, err)
			}
		}
		if e.CID != "" {
			h.Engine.Provide(ctx, e.CID)
		}
	}
}

// ServeHTTP routes by Host header: the bare domain carries the API, the
// directory, and claimed names as paths; subdomains are ENS, IPNS, or CIDs.
func hostnameOf(r *http.Request) string {
	hostname := strings.ToLower(r.Host)
	if i := strings.LastIndex(hostname, ":"); i > 0 && !strings.Contains(hostname[i:], "]") {
		hostname = hostname[:i]
	}
	return hostname
}

// signingHost is the hostname a request's signature must cover: the one the
// client addressed, or a trusted domain it was forwarded from.
func (h *Host) signingHost(r *http.Request) string {
	if fwd := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Croptop-Signed-Host"))); fwd != "" {
		for _, t := range h.Trust {
			if strings.EqualFold(strings.TrimSpace(t), fwd) {
				return fwd
			}
		}
	}
	return hostnameOf(r)
}

func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hostname := hostnameOf(r)
	switch {
	case hostname == h.Domain || hostname == "localhost" || hostname == "127.0.0.1":
		h.serveBare(w, r)
	case strings.HasSuffix(hostname, "."+h.Domain):
		h.serveLabel(w, r, strings.TrimSuffix(hostname, "."+h.Domain))
	default:
		http.Error(w, "unknown host", 404)
	}
}

func (h *Host) serveBare(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/routing/v1/ipns/"):
		h.serveRouting(w, r, strings.TrimSuffix(strings.TrimPrefix(p, "/routing/v1/ipns/"), "/"))
	case strings.HasPrefix(p, "/v0/host/"):
		h.serveAPI(w, r)
	case strings.HasPrefix(p, "/ipfs/") || strings.HasPrefix(p, "/ipns/"):
		// classic path gateway, so a host with one hostname (Railway, Fly) can
		// be an upstream for the Worker and a plain gateway for anyone
		h.servePath(w, r, p)
	case p == "/" && h.Root == "":
		h.serveDirectory(w, r)
	case p == "/directory" && h.Root != "":
		h.serveDirectory(w, r)
	default:
		name, rest, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
		h.mu.Lock()
		ipns := h.reg.Names[name]
		e := h.reg.Keys[ipns]
		h.mu.Unlock()
		if e == nil || e.CID == "" {
			if h.Root != "" { // not a claimed name: the root site owns the path
				c, err := h.resolveRoot(r.Context())
				if err != nil || c == "" {
					http.Error(w, "could not resolve "+html.EscapeString(h.Root)+": "+errText(err), 502)
					return
				}
				h.serveCID(w, r, c, p, "")
				return
			}
			http.Error(w, "no site named "+html.EscapeString(name)+" here", 404)
			return
		}
		if rest == "" && !strings.HasSuffix(p, "/") {
			http.Redirect(w, r, "/"+name+"/", 301)
			return
		}
		h.serveCID(w, r, e.CID, "/"+rest, "/"+name)
	}
}

func (h *Host) serveLabel(w http.ResponseWriter, r *http.Request, label string) {
	label = strings.ToLower(label)
	var c string
	var err error
	switch {
	case strings.HasPrefix(label, "bafy") || strings.HasPrefix(label, "qm"):
		c = label
	case strings.HasPrefix(label, "k51") || strings.HasPrefix(label, "k2k4"):
		c, err = h.resolveKey(r.Context(), label)
	default:
		c, err = h.resolveENS(r.Context(), label+".eth")
	}
	if err != nil || c == "" {
		http.Error(w, "could not resolve "+html.EscapeString(label)+": "+errText(err), 502)
		return
	}
	h.serveCID(w, r, c, r.URL.Path, "")
}

// resolveRoot turns Root (ENS name, IPNS name, or CID) into a CID.
func (h *Host) resolveRoot(ctx context.Context) (string, error) {
	return h.resolveRootLike(ctx, h.Root)
}

// servePath serves /ipfs/<cid>/... and /ipns/<name>/... on the bare domain.
func (h *Host) servePath(w http.ResponseWriter, r *http.Request, p string) {
	kind, rest, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	label, sub, _ := strings.Cut(rest, "/")
	if label == "" {
		http.Error(w, "missing name", 400)
		return
	}
	var c string
	var err error
	if kind == "ipfs" {
		c = label
	} else {
		c, err = h.resolveRootLike(r.Context(), label)
	}
	if err != nil || c == "" {
		http.Error(w, "could not resolve "+html.EscapeString(label)+": "+errText(err), 502)
		return
	}
	if sub == "" && !strings.HasSuffix(p, "/") {
		http.Redirect(w, r, p+"/", 301)
		return
	}
	h.serveCID(w, r, c, "/"+sub, "/"+kind+"/"+label)
}

// resolveRootLike resolves an ENS name, an IPNS name, or a CID to a CID.
func (h *Host) resolveRootLike(ctx context.Context, what string) (string, error) {
	what = strings.ToLower(strings.TrimSpace(what))
	switch {
	case strings.HasPrefix(what, "bafy") || strings.HasPrefix(what, "qm"):
		return what, nil
	case strings.HasPrefix(what, "k51") || strings.HasPrefix(what, "k2k4"):
		return h.resolveKey(ctx, what)
	default:
		return h.resolveENS(ctx, what)
	}
}

func errText(err error) string {
	if err == nil {
		return "no content"
	}
	return err.Error()
}

// resolveKey prefers the record a site pushed; otherwise asks the network.
func (h *Host) resolveKey(ctx context.Context, ipnsName string) (string, error) {
	h.mu.Lock()
	e := h.reg.Keys[ipnsName]
	h.mu.Unlock()
	if e != nil && e.CID != "" {
		return e.CID, nil
	}
	return h.resolveCached(ctx, "/ipns/"+ipnsName)
}

// resolveENS follows DNSLink for name.eth, then the IPNS name it points at.
func (h *Host) resolveENS(ctx context.Context, ens string) (string, error) {
	target, err := h.resolveCached(ctx, "/ipns/"+ens)
	if err != nil {
		return "", err
	}
	switch {
	case strings.HasPrefix(target, "/ipfs/"):
		return strings.TrimPrefix(target, "/ipfs/"), nil
	case strings.HasPrefix(target, "/ipns/"):
		return h.resolveKey(ctx, strings.TrimPrefix(target, "/ipns/"))
	}
	return "", fmt.Errorf("unexpected dnslink %q", target)
}

// resolveCached resolves one step with the engine and remembers it briefly.
func (h *Host) resolveCached(ctx context.Context, p string) (string, error) {
	h.mu.Lock()
	c, ok := h.cache[p]
	h.mu.Unlock()
	if ok && time.Since(c.at) < resolveCache {
		return c.cid, nil
	}
	rctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	v, err := h.Engine.Resolve(rctx, p)
	if err != nil {
		if ok { // stale beats nothing
			return c.cid, nil
		}
		return "", err
	}
	v = strings.TrimSuffix(v, "/")
	if strings.HasPrefix(p, "/ipns/k") {
		v = strings.TrimPrefix(v, "/ipfs/")
	}
	h.mu.Lock()
	h.cache[p] = cached{cid: v, at: time.Now()}
	h.mu.Unlock()
	return v, nil
}

// serveCID hands /ipfs/<cid><path> to the boxo gateway and hides the prefix
// from redirects it emits, so the site sees only its own base.
func (h *Host) serveCID(w http.ResponseWriter, r *http.Request, c, p, base string) {
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/ipfs/" + c + p
	r2.URL.RawPath = ""
	r2.Host = "" // stop the handler from thinking it is a subdomain gateway
	h.gw.ServeHTTP(&locationRewriter{ResponseWriter: w, from: "/ipfs/" + c, to: base}, r2)
}

type locationRewriter struct {
	http.ResponseWriter
	from, to string
}

func (l *locationRewriter) WriteHeader(code int) {
	if loc := l.Header().Get("Location"); strings.HasPrefix(loc, l.from) {
		l.Header().Set("Location", l.to+strings.TrimPrefix(loc, l.from))
	}
	l.ResponseWriter.WriteHeader(code)
}

func (h *Host) serveDirectory(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	names := make([]string, 0, len(h.reg.Names))
	for n := range h.reg.Names {
		names = append(names, n)
	}
	h.mu.Unlock()
	sort.Strings(names)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>%s</title><style>body{font:16px/1.5 system-ui;max-width:640px;margin:40px auto;padding:0 16px}li{margin:4px 0}</style>", html.EscapeString(h.Domain))
	fmt.Fprintf(w, "<h1>%s</h1><p>Sites published from croptop. ENS names live at <code>name.%s</code>; free names at <code>%s/name</code>.</p><ul>", html.EscapeString(h.Domain), html.EscapeString(h.Domain), html.EscapeString(h.Domain))
	for _, n := range names {
		fmt.Fprintf(w, `<li><a href="/%s/">%s</a></li>`, html.EscapeString(n), html.EscapeString(n))
	}
	fmt.Fprint(w, "</ul>")
}

// ---- API

func (h *Host) serveAPI(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/v0/host/")
	switch {
	case p == "health":
		// plain "ok" for probes; peer id and addresses for operators
		if r.URL.Query().Get("peer") == "" {
			w.Write([]byte("ok"))
			return
		}
		info, err := h.Engine.Info(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, info)
	case p == "peers" && r.Method == "GET":
		// what a new node dials to join the network through this host
		info, err := h.Engine.Info(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		addrs := h.Engine.PeerAddrs()
		if addrs == nil { // a node with no public address lists none, not null
			addrs = []string{}
		}
		writeJSON(w, 200, map[string]any{"id": info.PeerID, "addrs": addrs})
	case p == "names" && r.Method == "POST":
		h.claim(w, r)
	case p == "push" && r.Method == "POST":
		h.push(w, r)
	case p == "pull" && r.Method == "POST":
		h.pull(w, r)
	case strings.HasPrefix(p, "names/") && r.Method == "GET":
		h.mu.Lock()
		e := h.reg.Keys[h.reg.Names[strings.TrimPrefix(p, "names/")]]
		h.mu.Unlock()
		h.entry(w, e)
	case strings.HasPrefix(p, "keys/") && r.Method == "GET":
		h.mu.Lock()
		e := h.reg.Keys[strings.TrimPrefix(p, "keys/")]
		var pub Entry
		if e != nil {
			pub = *e
			pub.Record = nil
		}
		h.mu.Unlock()
		if e == nil {
			http.Error(w, "not found", 404)
			return
		}
		// acceptsParent: pushes of only what changed are understood here;
		// acceptsManifest: so are pushes that say what to keep, and so delete
		writeJSON(w, 200, struct {
			Entry
			AcceptsParent   bool `json:"acceptsParent"`
			AcceptsManifest bool `json:"acceptsManifest"`
		}{pub, true, true})
	case strings.HasPrefix(p, "versions/") && strings.HasSuffix(p, "/files") && r.Method == "GET":
		// what an unfinished push of this version left here, so its retry
		// sends only the rest
		c := strings.TrimSuffix(strings.TrimPrefix(p, "versions/"), "/files")
		if !cidRe.MatchString(c) {
			http.Error(w, "bad cid", 400)
			return
		}
		writeJSON(w, 200, stagedFiles(filepath.Join(h.DataDir, "host", "staging", c)))
	case strings.HasPrefix(p, "blocks/") && r.Method == "GET":
		// one raw block this host holds; clients check it against its CID
		b, err := h.Engine.Block(r.Context(), strings.TrimPrefix(p, "blocks/"))
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.ipld.raw")
		w.Write(b)
	default:
		http.Error(w, "not found", 404)
	}
}

// serveRouting is the IPNS part of the Delegated Routing V1 HTTP API, which
// delegated-ipfs.dev served until 2026-09-30: GET the newest record for a name,
// PUT a signed record into the DHT. Pushed sites answer from the registry.
// Anyone can call it, so DHT searches and puts each run in a bounded number of slots.
func (h *Host) serveRouting(w http.ResponseWriter, r *http.Request, name string) {
	if len(name) > 128 { // a name is a key of a few dozen characters, and decoding a long one is slow
		http.Error(w, "bad name", 400)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.mu.Lock()
		var rec []byte
		if e := h.reg.Keys[name]; e != nil {
			rec = e.Record
		}
		h.mu.Unlock()
		if len(rec) == 0 {
			slots := h.lookupSlots // the release must go back to the channel the slot came from
			select {
			case slots <- struct{}{}:
			default:
				w.Header().Set("Retry-After", "10")
				http.Error(w, "busy", 429)
				return
			}
			var err error
			rec, err = func() ([]byte, error) { // the slot is free again as soon as the search ends
				defer func() { <-slots }()
				// a record sent to this node is held locally and comes back first,
				// so 10 s suffices for those; the Worker waits 12 s for this node
				ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
				defer cancel()
				return h.Engine.GetRecord(ctx, name)
			}()
			if err != nil {
				http.Error(w, "not found", 404)
				return
			}
		}
		w.Header().Set("Content-Type", "application/vnd.ipfs.ipns-record")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Write(rec)
	case http.MethodPut:
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<10))
		if err != nil {
			http.Error(w, "record too large", 413)
			return
		}
		if err := ipfs.ValidateRecord(name, b); err != nil {
			http.Error(w, "invalid record: "+err.Error(), 400)
			return
		}
		slots := h.routingSlots // the release must go back to the channel the slot came from
		select {
		case slots <- struct{}{}:
		default:
			w.Header().Set("Retry-After", "10")
			http.Error(w, "busy", 429)
			return
		}
		go func() { // a DHT put takes seconds; the caller does not wait for it
			defer func() { <-slots }()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := h.Engine.RelayRecord(ctx, name, b); err != nil {
				h.log("routing put %s: %v", name, err)
			}
		}()
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Host) entry(w http.ResponseWriter, e *Entry) {
	if e == nil {
		http.Error(w, "not found", 404)
		return
	}
	pub := *e
	pub.Record = nil
	writeJSON(w, 200, pub)
}

// ClaimMessage is what a site signs to claim name for ipns at unix time t.
func ClaimMessage(domain, name, ipns string, t int64) []byte {
	return []byte(fmt.Sprintf("croptop-name\n%s\n%s\n%s\n%d", domain, name, ipns, t))
}

// PushMessage is what a site signs to push cid at seq for ipns at unix time t.
func PushMessage(domain, ipns, cid string, seq uint64, t int64) []byte {
	return []byte(fmt.Sprintf("croptop-push\n%s\n%s\n%s\n%d\n%d", domain, ipns, cid, seq, t))
}

func fresh(t int64) bool {
	d := time.Since(time.Unix(t, 0))
	return d < skew && d > -skew
}

func (h *Host) claim(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name, IPNS, Sig string
		Time            int64
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if !nameRe.MatchString(in.Name) || reserved[in.Name] {
		http.Error(w, "that name cannot be claimed", 400)
		return
	}
	sig, _ := base64.StdEncoding.DecodeString(in.Sig)
	// signed over the hostname the client addressed, which is the domain in production
	if !fresh(in.Time) || !ipfs.VerifyIPNS(in.IPNS, ClaimMessage(h.signingHost(r), in.Name, in.IPNS, in.Time), sig) {
		http.Error(w, "bad signature", 403)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if owner, taken := h.reg.Names[in.Name]; taken && owner != in.IPNS {
		http.Error(w, "that name is taken", 409)
		return
	}
	e := h.reg.Keys[in.IPNS]
	if e == nil {
		e = &Entry{IPNS: in.IPNS}
		h.reg.Keys[in.IPNS] = e
	}
	if e.Name != "" && e.Name != in.Name {
		delete(h.reg.Names, e.Name) // one name per key: renaming releases the old one
	}
	e.Name = in.Name
	e.Updated = time.Now()
	h.reg.Names[in.Name] = in.IPNS
	if err := h.save(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h.log("%s claimed by %s", in.Name, in.IPNS)
	pub := *e
	pub.Record = nil
	writeJSON(w, 200, pub)
}

func (h *Host) push(w http.ResponseWriter, r *http.Request) {
	ipnsName, c := r.Header.Get("X-Croptop-Ipns"), r.Header.Get("X-Croptop-Cid")
	seq, _ := strconv.ParseUint(r.Header.Get("X-Croptop-Seq"), 10, 64)
	t, _ := strconv.ParseInt(r.Header.Get("X-Croptop-Time"), 10, 64)
	sig, _ := base64.StdEncoding.DecodeString(r.Header.Get("X-Croptop-Sig"))
	if !cidRe.MatchString(c) {
		http.Error(w, "bad cid", 400)
		return
	}
	if !fresh(t) || !ipfs.VerifyIPNS(ipnsName, PushMessage(h.signingHost(r), ipnsName, c, seq, t), sig) {
		http.Error(w, "bad signature", 403)
		return
	}
	var rec []byte
	if b64 := r.Header.Get("X-Croptop-Record"); b64 != "" {
		rec, _ = base64.StdEncoding.DecodeString(b64)
	}
	parent := r.Header.Get("X-Croptop-Parent")
	h.mu.Lock()
	msg := conflict(h.reg.Keys[ipnsName], seq, c, parent)
	h.mu.Unlock()
	if msg != "" {
		http.Error(w, msg, 409)
		return
	}
	// The files land in a staging dir for this cid; a big site arrives in
	// "i/n" parts. On the last part they are added like any site and the root
	// must hash to the cid the request was signed for. If a file was too big
	// for the sender to carry, the missing blocks are fetched from the
	// network, which verifies the tree by construction. With a parent the
	// files are only what changed, and they go on top of the parent. A
	// manifest in the last part lists the parent's paths to keep instead, and
	// the rest of the parent is dropped.
	stage := filepath.Join(h.DataDir, "host", "staging", c)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if rel := r.Header.Get("X-Croptop-File"); rel != "" {
		// one chunk of a file too big for a single request
		if err := saveChunk(r, stage, rel); err != nil {
			http.Error(w, "chunk: "+err.Error(), 400)
			return
		}
		writeJSON(w, 200, map[string]any{"chunk": r.Header.Get("X-Croptop-Chunk")})
		return
	}
	n, manifest, err := saveMultipart(r, stage)
	if errors.Is(err, errManifestTooLarge) {
		http.Error(w, err.Error(), 400)
		return
	}
	if err != nil {
		http.Error(w, "reading files: "+err.Error(), 400)
		return
	}
	var partNo, partCount int
	fmt.Sscanf(r.Header.Get("X-Croptop-Part"), "%d/%d", &partNo, &partCount)
	if partCount > 0 && partNo < partCount {
		if manifest != "" {
			// it names what the whole version keeps, so it goes in the part that commits it
			http.Error(w, "the manifest goes in the final part", 400)
			return
		}
		writeJSON(w, 200, map[string]any{"part": partNo, "of": partCount, "files": n})
		return
	}
	var carry []string
	if manifest != "" {
		var m struct {
			Carry []string `json:"carry"`
		}
		if err := json.Unmarshal([]byte(manifest), &m); err != nil || parent == "" {
			http.Error(w, "a manifest needs a parent and a carry list", 400)
			return
		}
		carry = append([]string{}, m.Carry...)
	}
	defer os.RemoveAll(stage)
	var got string
	switch {
	case carry != nil:
		// the version is the uploaded files plus the carried paths: nothing else.
		// A parent block this host lacks is waited for, so the rebuild is bounded.
		rctx, cancel := context.WithTimeout(r.Context(), h.rebuildTimeout)
		got, err = h.Engine.Rebuild(rctx, parent, stage, carry)
		cancel()
		switch {
		case errors.Is(err, ipfs.ErrBadManifest):
			http.Error(w, "manifest: "+err.Error(), 400)
			return
		case err != nil:
			// the host's own failure: what it says (a path on this disk, say) is for its log
			h.log("push %s: rebuilding %s: %v", ipnsName, c, err)
			http.Error(w, "manifest: could not rebuild", 500)
			return
		}
		if got != c {
			http.Error(w, fmt.Sprintf("the files and carried paths make %s, not %s", got, c), 400)
			return
		}
	case parent != "":
		got, err = h.Engine.AddOver(r.Context(), parent, stage)
	default:
		got, err = h.Engine.AddDir(r.Context(), stage)
	}
	if err != nil {
		http.Error(w, "adding files: "+err.Error(), 500)
		return
	}
	if got != c {
		h.log("push %s: files hash to %s, fetching the rest of %s from the network", ipnsName, got, c)
		fctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		err = h.Engine.Get(fctx, "/ipfs/"+c, filepath.Join(stage, "..", c+".fetch"))
		cancel()
		os.RemoveAll(filepath.Join(stage, "..", c+".fetch"))
		if err != nil {
			http.Error(w, fmt.Sprintf("files hash to %s, not %s, and the rest could not be fetched: %v", got, c, err), 400)
			return
		}
	}
	h.mu.Lock()
	if msg := conflict(h.reg.Keys[ipnsName], seq, c, parent); msg != "" {
		h.mu.Unlock()
		http.Error(w, msg, 409)
		return
	}
	e := h.reg.Keys[ipnsName]
	if e == nil {
		e = &Entry{IPNS: ipnsName}
		h.reg.Keys[ipnsName] = e
	}
	e.CID, e.Sequence, e.Updated = c, seq, time.Now()
	if len(rec) > 0 {
		e.Record = rec
	}
	err = h.save()
	h.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h.log("push %s: %d blocks, sequence %d -> %s", ipnsName, n, seq, c)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		h.Engine.Provide(ctx, c)
		if len(rec) > 0 {
			if err := h.Engine.PutRecord(ctx, ipnsName, rec); err != nil {
				h.log("record for %s: %v", ipnsName, err)
			}
		}
	}()
	writeJSON(w, 200, map[string]any{"cid": c, "sequence": seq, "blocks": n, "name": e.Name})
}

// conflict says why a push of c at seq cannot replace e, or "". A push with a
// parent was built on that version, so it must still be the one held here:
// otherwise the push would drop whatever replaced it. A whole version at the
// sequence of another one would replace it unseen: two machines that chose the
// same next sequence, a laptop and an agent say.
func conflict(e *Entry, seq uint64, c, parent string) string {
	switch {
	case e != nil && seq < e.Sequence:
		return fmt.Sprintf("host already has sequence %d", e.Sequence)
	case parent == "" && e != nil && e.CID != "" && seq == e.Sequence && e.CID != c:
		return fmt.Sprintf("host holds %s at sequence %d", e.CID, seq)
	case parent != "" && (e == nil || e.CID != parent):
		have := "nothing"
		if e != nil && e.CID != "" {
			have = e.CID
		}
		return fmt.Sprintf("host holds %s, not %s", have, parent)
	}
	return ""
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// saveMultipart writes each "file:<path>" part to dir at that path. The path
// rides in the field name because Go's multipart reader strips directories
// from file names. With X-Croptop-Encoding: base64 the parts are base64 text,
// which lets a forwarded push cross a web application firewall that would
// otherwise read a site's HTML and scripts as an attack. The "manifest" field,
// if there is one, comes back as the text it was sent as; one over maxManifest
// is an error.
func saveMultipart(r *http.Request, dir string) (int, string, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxPush)
	mr, err := r.MultipartReader()
	if err != nil {
		return 0, "", err
	}
	b64 := strings.EqualFold(r.Header.Get("X-Croptop-Encoding"), "base64")
	n := 0
	var manifest string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return n, manifest, nil
		}
		if err != nil {
			return n, manifest, err
		}
		if part.FormName() == "manifest" {
			b, err := io.ReadAll(io.LimitReader(part, maxManifest+1))
			if err != nil {
				return n, manifest, err
			}
			if len(b) > maxManifest { // cut short it would be read as some other manifest, or none
				return n, manifest, errManifestTooLarge
			}
			manifest = string(b)
			continue
		}
		if !strings.HasPrefix(part.FormName(), "file:") {
			continue
		}
		rel := filepath.Clean("/" + filepath.FromSlash(strings.TrimPrefix(part.FormName(), "file:")))
		dst := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return n, manifest, err
		}
		f, err := os.Create(dst)
		if err != nil {
			return n, manifest, err
		}
		var src io.Reader = part
		if b64 {
			src = base64.NewDecoder(base64.StdEncoding, part)
		}
		_, err = io.Copy(f, src)
		f.Close()
		if err != nil {
			return n, manifest, err
		}
		n++
	}
}

// saveChunk stores chunk i/n of a file under stage and joins the chunks when
// the last one lands. Chunks may arrive in any order.
func saveChunk(r *http.Request, stage, rel string) error {
	var i, n int
	if _, err := fmt.Sscanf(r.Header.Get("X-Croptop-Chunk"), "%d/%d", &i, &n); err != nil || i < 1 || n < i {
		return fmt.Errorf("bad chunk header")
	}
	dst := filepath.Join(stage, filepath.Clean("/"+filepath.FromSlash(rel)))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	var src io.Reader = http.MaxBytesReader(nil, r.Body, maxPush)
	if strings.EqualFold(r.Header.Get("X-Croptop-Encoding"), "base64") {
		src = base64.NewDecoder(base64.StdEncoding, src)
	}
	part := fmt.Sprintf("%s.part%d", dst, i)
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, src)
	f.Close()
	if err != nil {
		return err
	}
	// join once every chunk is here
	for j := 1; j <= n; j++ {
		if _, err := os.Stat(fmt.Sprintf("%s.part%d", dst, j)); err != nil {
			return nil
		}
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	for j := 1; j <= n; j++ {
		name := fmt.Sprintf("%s.part%d", dst, j)
		in, err := os.Open(name)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		os.Remove(name)
		if err != nil {
			return err
		}
	}
	return nil
}

// pull mirrors a version another host holds: the request is signed like a
// push and names a base URL and the files under it. The download runs in the
// background; the root must hash to the cid, with missing blocks fetched
// from the network as a last resort.
func (h *Host) pull(w http.ResponseWriter, r *http.Request) {
	ipnsName, c := r.Header.Get("X-Croptop-Ipns"), r.Header.Get("X-Croptop-Cid")
	seq, _ := strconv.ParseUint(r.Header.Get("X-Croptop-Seq"), 10, 64)
	t, _ := strconv.ParseInt(r.Header.Get("X-Croptop-Time"), 10, 64)
	sig, _ := base64.StdEncoding.DecodeString(r.Header.Get("X-Croptop-Sig"))
	if !cidRe.MatchString(c) {
		http.Error(w, "bad cid", 400)
		return
	}
	if !fresh(t) || !ipfs.VerifyIPNS(ipnsName, PushMessage(h.signingHost(r), ipnsName, c, seq, t), sig) {
		http.Error(w, "bad signature", 403)
		return
	}
	var rec []byte
	if b64 := r.Header.Get("X-Croptop-Record"); b64 != "" {
		rec, _ = base64.StdEncoding.DecodeString(b64)
	}
	var in struct {
		Base  string
		Files []string
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&in); err != nil || in.Base == "" {
		http.Error(w, "bad request", 400)
		return
	}
	h.mu.Lock()
	if e := h.reg.Keys[ipnsName]; e != nil && seq < e.Sequence {
		h.mu.Unlock()
		http.Error(w, fmt.Sprintf("host already has sequence %d", e.Sequence), 409)
		return
	}
	h.mu.Unlock()
	writeJSON(w, 202, map[string]any{"pulling": c, "files": len(in.Files)})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := h.mirror(ctx, ipnsName, c, seq, rec, in.Base, in.Files); err != nil {
			h.log("pull %s: %v", c, err)
		}
	}()
}

func (h *Host) mirror(ctx context.Context, ipnsName, c string, seq uint64, rec []byte, base string, files []string) error {
	if !cidRe.MatchString(c) {
		return fmt.Errorf("bad cid %q", c)
	}
	stage := filepath.Join(h.DataDir, "host", "staging", c)
	defer os.RemoveAll(stage)
	client := &http.Client{Timeout: 20 * time.Minute}
	for _, rel := range files {
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" || strings.Contains(rel, "..") {
			continue
		}
		dst := filepath.Join(stage, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(base, "/")+"/"+rel, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return fmt.Errorf("%s: %s", rel, resp.Status)
		}
		f, err := os.Create(dst)
		if err != nil {
			resp.Body.Close()
			return err
		}
		_, err = io.Copy(f, resp.Body)
		f.Close()
		resp.Body.Close()
		if err != nil {
			return err
		}
	}
	got, err := h.Engine.AddDir(ctx, stage)
	if err != nil {
		return err
	}
	if got != c {
		h.log("pull %s: files hash to %s, fetching the rest from the network", c, got)
		if err := h.Engine.Get(ctx, "/ipfs/"+c, filepath.Join(stage, "..", c+".fetch")); err != nil {
			return fmt.Errorf("files hash to %s, not %s, and the rest could not be fetched: %w", got, c, err)
		}
		os.RemoveAll(filepath.Join(stage, "..", c+".fetch"))
	}
	h.mu.Lock()
	e := h.reg.Keys[ipnsName]
	if e == nil {
		e = &Entry{IPNS: ipnsName}
		h.reg.Keys[ipnsName] = e
	}
	if seq >= e.Sequence {
		e.CID, e.Sequence, e.Updated = c, seq, time.Now()
		if len(rec) > 0 {
			e.Record = rec
		}
	}
	err = h.save()
	h.mu.Unlock()
	if err != nil {
		return err
	}
	h.log("mirrored %s: %d files, sequence %d -> %s", ipnsName, len(files), seq, c)
	h.Engine.Provide(ctx, c)
	if len(rec) > 0 {
		if err := h.Engine.PutRecord(ctx, ipnsName, rec); err != nil {
			h.log("record for %s: %v", ipnsName, err)
		}
	}
	return nil
}

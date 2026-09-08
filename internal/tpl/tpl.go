// Package tpl resolves which template renders a site and lets owners fork,
// edit, install, and publish templates. A template is a directory with
// template.json, templates/, and assets/, the layout of SiteTemplateCroptop.
package tpl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

// Choice is the site's template setting (site JSON key croptopTemplate,
// ignored by Planet).
type Choice struct {
	CID        string `json:"cid,omitempty"`        // installed template in use, "" = embedded default
	Upstream   string `json:"upstream,omitempty"`   // ENS or IPNS name the template came from, for updates
	Forked     bool   `json:"forked"`               // sites/<id>/template/ holds an editable copy
	ForkedFrom string `json:"forkedFrom,omitempty"` // CID the fork started from ("" = the embedded default)
}

const SettingKey = "croptopTemplate"

func ChoiceOf(site *store.Site) Choice {
	var c Choice
	if raw, ok := site.Raw[SettingKey]; ok {
		json.Unmarshal(raw, &c)
	}
	return c
}

func SetChoice(site *store.Site, c Choice) {
	if site.Raw == nil {
		site.Raw = map[string]json.RawMessage{}
	}
	b, _ := json.Marshal(c)
	site.Raw[SettingKey] = b
}

type Info struct {
	CID     string   `json:"cid"` // "" for the embedded default
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Build   int      `json:"buildNumber"`
	Default bool     `json:"default"`
	Sites   []string `json:"sites"` // site ids using it
}

type Resolver struct {
	DataDir string
	Store   *store.Store
	Default fs.FS
	Engine  ipfs.Engine
	Log     func(string)
}

func (r *Resolver) log(format string, a ...any) {
	if r.Log != nil {
		r.Log(fmt.Sprintf(format, a...))
	}
}

func (r *Resolver) ForkDir(siteID string) string {
	return filepath.Join(r.Store.SiteDir(siteID), "template")
}
func (r *Resolver) InstalledDir(cid string) string { return filepath.Join(r.DataDir, "templates", cid) }

// For returns the template that renders site and where it came from:
// "fork", "installed:<cid>", or "default".
func (r *Resolver) For(site *store.Site) (fs.FS, string, error) {
	c := ChoiceOf(site)
	if c.Forked {
		dir := r.ForkDir(site.ID)
		if _, err := os.Stat(filepath.Join(dir, "template.json")); err == nil {
			return os.DirFS(dir), "fork", nil
		}
	}
	if c.CID != "" {
		dir := r.InstalledDir(c.CID)
		if _, err := os.Stat(filepath.Join(dir, "template.json")); err == nil {
			return os.DirFS(dir), "installed:" + c.CID, nil
		}
		return nil, "", fmt.Errorf("template %s is not installed on this machine", c.CID)
	}
	return r.Default, "default", nil
}

// TemplateFor is the renderer hook.
func (r *Resolver) TemplateFor(site *store.Site) (fs.FS, error) {
	fsys, _, err := r.For(site)
	return fsys, err
}

func info(fsys fs.FS) Info {
	var m struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Build   int    `json:"buildNumber"`
	}
	if b, err := fs.ReadFile(fsys, "template.json"); err == nil {
		json.Unmarshal(b, &m)
	}
	return Info{Name: m.Name, Version: m.Version, Build: m.Build}
}

// Installed lists the embedded default and every installed template.
func (r *Resolver) Installed() ([]Info, error) {
	def := info(r.Default)
	def.Default = true
	out := []Info{def}
	entries, _ := os.ReadDir(filepath.Join(r.DataDir, "templates"))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		i := info(os.DirFS(r.InstalledDir(e.Name())))
		i.CID = e.Name()
		out = append(out, i)
	}
	sites, _ := r.Store.Sites()
	for idx := range out {
		out[idx].Sites = []string{}
		for _, s := range sites {
			if ChoiceOf(s).CID == out[idx].CID {
				out[idx].Sites = append(out[idx].Sites, s.ID)
			}
		}
	}
	return out, nil
}

// Validate checks a template can render a site.
func Validate(fsys fs.FS) error {
	if _, err := render.LoadMeta(fsys); err != nil {
		return err
	}
	if _, err := fs.Stat(fsys, "templates/index.html"); err != nil {
		return errors.New("template has no templates/index.html")
	}
	tmp, err := os.MkdirTemp("", "tpl-validate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	st := &store.Store{Root: tmp}
	site := &store.Site{ID: "00000000-0000-4000-8000-000000000000", Name: "Check", About: "check", IPNS: "k51check", TemplateName: "Croptop", Created: store.Now(), Updated: store.Now()}
	if err := st.SaveSite(site); err != nil {
		return err
	}
	empty := ""
	post := &store.Post{ID: "00000000-0000-4000-8000-000000000001", Title: "Hello", Content: "world", Created: store.Now(), Link: "/00000000-0000-4000-8000-000000000001/", Attachments: []string{}, Tags: map[string]string{"t": "t"}, Summary: &empty}
	if err := st.SavePost(site.ID, post); err != nil {
		return err
	}
	rr := &render.Renderer{Store: st, Templates: fsys, CIDs: fakeCIDs{}}
	if err := rr.Render(context.Background(), site.ID); err != nil {
		return fmt.Errorf("template fails to render: %w", err)
	}
	return nil
}

type fakeCIDs struct{}

func (fakeCIDs) FileCIDv0(ctx context.Context, p string) (string, error) {
	return "QmValidate" + strings.ToUpper(filepath.Base(p)), nil
}

// Install fetches a template by CID, IPNS name, or ENS name and stores it.
func (r *Resolver) Install(ctx context.Context, nameOrCID string) (Info, error) {
	cid, err := r.resolveCID(ctx, nameOrCID)
	if err != nil {
		return Info{}, err
	}
	dir := r.InstalledDir(cid)
	if _, err := os.Stat(filepath.Join(dir, "template.json")); err == nil {
		i := info(os.DirFS(dir))
		i.CID = cid
		return i, nil
	}
	tmp := dir + ".new"
	os.RemoveAll(tmp)
	if err := r.Engine.Get(ctx, "/ipfs/"+cid, tmp); err != nil {
		return Info{}, fmt.Errorf("fetch template %s: %w", cid, err)
	}
	if err := Validate(os.DirFS(tmp)); err != nil {
		os.RemoveAll(tmp)
		return Info{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return Info{}, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return Info{}, err
	}
	r.Engine.Provide(ctx, cid)
	i := info(os.DirFS(dir))
	i.CID = cid
	return i, nil
}

func (r *Resolver) resolveCID(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/ipfs/")
	if strings.HasPrefix(name, "bafy") || strings.HasPrefix(name, "Qm") {
		return name, nil
	}
	p := "/ipns/" + strings.TrimPrefix(name, "/ipns/")
	for i := 0; i < 3; i++ { // ENS -> IPNS -> CID
		resolved, err := r.Engine.Resolve(ctx, p)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(resolved, "/ipfs/") {
			return strings.TrimPrefix(resolved, "/ipfs/"), nil
		}
		p = resolved
	}
	return "", fmt.Errorf("%s did not resolve to a CID", name)
}

// Publish adds a template directory to IPFS and returns its CID.
func (r *Resolver) Publish(ctx context.Context, dir string) (string, error) {
	if err := Validate(os.DirFS(dir)); err != nil {
		return "", err
	}
	cid, err := r.Engine.AddDir(ctx, dir)
	if err != nil {
		return "", err
	}
	r.Engine.Provide(ctx, cid)
	// keep a local copy so the publisher can use it by CID too
	if _, err := os.Stat(r.InstalledDir(cid)); err != nil {
		os.MkdirAll(filepath.Dir(r.InstalledDir(cid)), 0o755)
		os.CopyFS(r.InstalledDir(cid), os.DirFS(dir))
	}
	return cid, nil
}

// Use points a site at an installed template ("" for the default). A fork
// is left in place and still wins until Reset.
func (r *Resolver) Use(siteID, cid string) error {
	site, err := r.Store.Site(siteID)
	if err != nil {
		return err
	}
	if cid != "" {
		if _, err := os.Stat(filepath.Join(r.InstalledDir(cid), "template.json")); err != nil {
			return fmt.Errorf("template %s is not installed", cid)
		}
	}
	c := ChoiceOf(site)
	c.CID = cid
	SetChoice(site, c)
	return r.Store.SaveSite(site)
}

// Fork copies the site's current template into the site for editing.
func (r *Resolver) Fork(siteID string) error {
	site, err := r.Store.Site(siteID)
	if err != nil {
		return err
	}
	c := ChoiceOf(site)
	if c.Forked {
		return errors.New("already forked")
	}
	src, _, err := r.For(site)
	if err != nil {
		return err
	}
	dir := r.ForkDir(siteID)
	os.RemoveAll(dir)
	if err := os.CopyFS(dir, src); err != nil {
		return err
	}
	c.Forked, c.ForkedFrom = true, c.CID
	SetChoice(site, c)
	return r.Store.SaveSite(site)
}

// Reset discards the fork; the site goes back to the installed or default template.
func (r *Resolver) Reset(siteID string) error {
	site, err := r.Store.Site(siteID)
	if err != nil {
		return err
	}
	os.RemoveAll(r.ForkDir(siteID))
	c := ChoiceOf(site)
	c.Forked, c.ForkedFrom = false, ""
	SetChoice(site, c)
	return r.Store.SaveSite(site)
}

var editable = map[string]bool{".html": true, ".css": true, ".js": true, ".json": true, ".md": true, ".txt": true, ".svg": true, ".xml": true}

// Files lists the template's files (fork or not), editable text files first.
func (r *Resolver) Files(site *store.Site) ([]string, error) {
	fsys, _, err := r.For(site)
	if err != nil {
		return nil, err
	}
	var out []string
	fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(p, ".") || strings.HasPrefix(p, "dev/") || strings.HasPrefix(p, "hooks/") {
			return nil
		}
		out = append(out, p)
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool {
		ei, ej := editable[path.Ext(out[i])], editable[path.Ext(out[j])]
		if ei != ej {
			return ei
		}
		return out[i] < out[j]
	})
	return out, nil
}

func Editable(p string) bool { return editable[path.Ext(p)] }

// safePath accepts template.json and paths under templates/ or assets/ only.
func safePath(p string) (string, error) {
	if strings.Contains(p, "..") || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return "", errors.New("bad path")
	}
	p = path.Clean(p)
	if p == "template.json" || strings.HasPrefix(p, "templates/") || strings.HasPrefix(p, "assets/") {
		return p, nil
	}
	return "", fmt.Errorf("%s is outside the template", p)
}

func (r *Resolver) ReadFile(site *store.Site, p string) ([]byte, error) {
	p, err := safePath(p)
	if err != nil {
		return nil, err
	}
	fsys, _, err := r.For(site)
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(fsys, p)
}

// WriteFile edits a forked template's file.
func (r *Resolver) WriteFile(site *store.Site, p string, b []byte) error {
	p, err := safePath(p)
	if err != nil {
		return err
	}
	if !ChoiceOf(site).Forked {
		return errors.New("fork the template before editing it")
	}
	if !Editable(p) {
		return fmt.Errorf("%s is not a text file", p)
	}
	full := filepath.Join(r.ForkDir(site.ID), filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, b, 0o644)
}

// UpstreamStatus compares a fork with the newest version of its upstream.
type UpstreamStatus struct {
	Upstream   string   `json:"upstream"`
	ForkedFrom string   `json:"forkedFrom"`
	Current    string   `json:"current"`
	Changed    []string `json:"changed"` // files that differ between forkedFrom and current
	Edited     []string `json:"edited"`  // files the fork changed relative to forkedFrom
}

func (r *Resolver) Upstream(ctx context.Context, site *store.Site) (UpstreamStatus, error) {
	c := ChoiceOf(site)
	st := UpstreamStatus{Upstream: c.Upstream, ForkedFrom: c.ForkedFrom, Changed: []string{}, Edited: []string{}}
	if !c.Forked {
		return st, errors.New("site has no fork")
	}
	base := r.Default
	if c.ForkedFrom != "" {
		base = os.DirFS(r.InstalledDir(c.ForkedFrom))
	}
	fork := os.DirFS(r.ForkDir(site.ID))
	st.Edited = differing(base, fork)
	if c.Upstream == "" {
		return st, nil
	}
	cur, err := r.Install(ctx, c.Upstream)
	if err != nil {
		return st, err
	}
	st.Current = cur.CID
	if cur.CID != c.ForkedFrom {
		st.Changed = differing(base, os.DirFS(r.InstalledDir(cur.CID)))
	}
	return st, nil
}

// TakeTheirs copies one file from the newest upstream into the fork.
func (r *Resolver) TakeTheirs(ctx context.Context, site *store.Site, p string) error {
	st, err := r.Upstream(ctx, site)
	if err != nil {
		return err
	}
	if st.Current == "" {
		return errors.New("no upstream")
	}
	b, err := fs.ReadFile(os.DirFS(r.InstalledDir(st.Current)), p)
	if err != nil {
		return err
	}
	return r.WriteFile(site, p, b)
}

func differing(a, b fs.FS) []string {
	seen := map[string]bool{}
	var out []string
	walk := func(fsys fs.FS, other fs.FS) {
		fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || seen[p] {
				return nil
			}
			seen[p] = true
			x, _ := fs.ReadFile(fsys, p)
			y, err := fs.ReadFile(other, p)
			if err != nil || string(x) != string(y) {
				out = append(out, p)
			}
			return nil
		})
	}
	walk(a, b)
	walk(b, a)
	sort.Strings(out)
	return out
}

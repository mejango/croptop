package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/store"
)

const (
	avatarLimit     = 5 << 20
	attachmentLimit = 50 << 20
)

// routesAPI is Planet's REST API (Technotes/API.md), so `pn` and other
// Planet clients work against Croptop unchanged.
func (s *Server) routesAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /v0/ping", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("pong")) })
	mux.HandleFunc("GET /v0/id", func(w http.ResponseWriter, r *http.Request) {
		info, _ := s.Node.Info(r.Context())
		w.Write([]byte(info.PeerID))
	})
	mux.HandleFunc("GET /v0/info", func(w http.ResponseWriter, r *http.Request) {
		info, _ := s.Node.Info(r.Context())
		host, _ := os.Hostname()
		writeJSON(w, 200, map[string]any{
			"hostName": host, "version": s.Version, "ipfsPeerID": info.PeerID,
			"ipfsPeerCount": info.Peers, "ipfsVersion": info.Version,
		})
	})

	mux.HandleFunc("GET /v0/planets/my", func(w http.ResponseWriter, r *http.Request) {
		sites, err := s.Store.Sites()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		archived, all := r.URL.Query().Get("archived") == "true", r.URL.Query().Get("all") == "true"
		out := []*store.Site{}
		for _, site := range sites {
			if all || site.IsArchived() == archived {
				out = append(out, site)
			}
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("POST /v0/planets/my", s.createSite)
	mux.HandleFunc("GET /v0/planets/my/{id}", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if ok {
			writeJSON(w, 200, site)
		}
	})
	mux.HandleFunc("POST /v0/planets/my/{id}", s.modifySite)
	mux.HandleFunc("DELETE /v0/planets/my/{id}", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		if err := s.Store.DeleteSite(site.ID); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]string{"deleted": site.ID})
	})
	mux.HandleFunc("POST /v0/planets/my/{id}/publish", func(w http.ResponseWriter, r *http.Request) {
		s.publishSite(w, r, r.URL.Query().Get("force") == "true")
	})
	mux.HandleFunc("GET /v0/planets/my/{id}/public", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/"+strings.ToUpper(r.PathValue("id"))+"/", http.StatusFound)
	})

	mux.HandleFunc("GET /v0/planets/my/{id}/articles", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		posts, err := s.Store.Posts(site.ID)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		if posts == nil {
			posts = []*store.Post{}
		}
		writeJSON(w, 200, posts)
	})
	mux.HandleFunc("POST /v0/planets/my/{id}/articles", s.createPost)
	mux.HandleFunc("GET /v0/planets/my/{id}/articles/{post}", func(w http.ResponseWriter, r *http.Request) {
		_, post, ok := s.post(w, r)
		if ok {
			writeJSON(w, 200, post)
		}
	})
	mux.HandleFunc("POST /v0/planets/my/{id}/articles/{post}", s.modifyPost)
	mux.HandleFunc("DELETE /v0/planets/my/{id}/articles/{post}", func(w http.ResponseWriter, r *http.Request) {
		site, post, ok := s.post(w, r)
		if !ok {
			return
		}
		if err := s.Store.DeletePost(site.ID, post.ID); err != nil {
			writeErr(w, 500, err)
			return
		}
		os.RemoveAll(filepath.Join(s.Store.PublicDir(site.ID), post.ID))
		if post.Dir() != post.ID {
			os.RemoveAll(filepath.Join(s.Store.PublicDir(site.ID), post.Dir()))
		}
		s.render(r.Context(), site.ID)
		writeJSON(w, 200, map[string]string{"deleted": post.ID})
	})
	mux.HandleFunc("GET /v0/planets/my/{id}/articles/{post}/attachments", func(w http.ResponseWriter, r *http.Request) {
		_, post, ok := s.post(w, r)
		if ok {
			writeJSON(w, 200, post.Attachments)
		}
	})
	mux.HandleFunc("POST /v0/planets/my/{id}/articles/{post}/attachments", func(w http.ResponseWriter, r *http.Request) {
		site, post, ok := s.post(w, r)
		if !ok {
			return
		}
		if err := r.ParseMultipartForm(attachmentLimit); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := s.saveAttachments(site.ID, post, r.MultipartForm, "append"); err != nil {
			writeErr(w, 500, err)
			return
		}
		s.savePostAndRender(w, r, site.ID, post)
	})
	mux.HandleFunc("DELETE /v0/planets/my/{id}/articles/{post}/attachments/{name}", func(w http.ResponseWriter, r *http.Request) {
		site, post, ok := s.post(w, r)
		if !ok {
			return
		}
		name := r.PathValue("name")
		kept := post.Attachments[:0]
		for _, a := range post.Attachments {
			if a != name {
				kept = append(kept, a)
			}
		}
		post.Attachments = kept
		delete(post.CIDs, name)
		os.Remove(filepath.Join(s.Store.PostDir(site.ID, post.ID), name))
		os.Remove(filepath.Join(s.Store.PublicDir(site.ID), post.ID, name))
		s.savePostAndRender(w, r, site.ID, post)
	})

	mux.HandleFunc("GET /v0/search", func(w http.ResponseWriter, r *http.Request) {
		q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
		sites, _ := s.Store.Sites()
		hitSites, hitPosts := []*store.Site{}, []map[string]any{}
		for _, site := range sites {
			if q != "" && (strings.Contains(strings.ToLower(site.Name), q) || strings.Contains(strings.ToLower(site.About), q)) {
				hitSites = append(hitSites, site)
			}
			posts, _ := s.Store.Posts(site.ID)
			for _, p := range posts {
				if q != "" && (strings.Contains(strings.ToLower(p.Title), q) || strings.Contains(strings.ToLower(p.Content), q)) {
					hitPosts = append(hitPosts, map[string]any{"planetID": site.ID, "planetName": site.Name, "article": p})
				}
			}
		}
		writeJSON(w, 200, map[string]any{"planets": hitSites, "articles": hitPosts})
	})
}

func (s *Server) site(w http.ResponseWriter, r *http.Request) (*store.Site, bool) {
	site, err := s.Store.Site(strings.ToUpper(r.PathValue("id")))
	if err != nil {
		writeErr(w, 404, errors.New("planet not found"))
		return nil, false
	}
	return site, true
}

func (s *Server) post(w http.ResponseWriter, r *http.Request) (*store.Site, *store.Post, bool) {
	site, ok := s.site(w, r)
	if !ok {
		return nil, nil, false
	}
	post, err := s.Store.Post(site.ID, strings.ToUpper(r.PathValue("post")))
	if err != nil {
		writeErr(w, 404, errors.New("article not found"))
		return nil, nil, false
	}
	return site, post, true
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

func (s *Server) createSite(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(avatarLimit); err != nil {
		writeErr(w, 400, err)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		writeErr(w, 400, errors.New("name is required"))
		return
	}
	id := newUUID()
	ipns, err := s.Node.Keystore().Generate(id)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	now := store.Now()
	f := false
	site := &store.Site{
		ID: id, Name: name, About: r.FormValue("about"), IPNS: ipns, TemplateName: "Croptop",
		Created: now, Updated: now, Archived: &f, Tags: map[string]string{},
	}
	if err := s.Store.SaveSite(site); err != nil {
		writeErr(w, 500, err)
		return
	}
	if meta, err := s.meta(); err == nil {
		s.Store.SaveTemplateSettings(id, meta.SettingsWithDefaults(nil))
	}
	if err := s.saveAvatar(site.ID, r.MultipartForm); err != nil {
		writeErr(w, 400, err)
		return
	}
	s.render(r.Context(), site.ID)
	writeJSON(w, 200, site)
}

func (s *Server) modifySite(w http.ResponseWriter, r *http.Request) {
	site, ok := s.site(w, r)
	if !ok {
		return
	}
	if site.IsArchived() {
		writeErr(w, 400, errors.New("Planet is archived."))
		return
	}
	if err := r.ParseMultipartForm(avatarLimit); err != nil {
		writeErr(w, 400, err)
		return
	}
	if v, ok := formValue(r, "name"); ok && strings.TrimSpace(v) != "" {
		site.Name = v
	}
	if v, ok := formValue(r, "about"); ok {
		site.About = v
	}
	if v, ok := formValue(r, "domain"); ok {
		site.Domain = &v
	}
	site.Updated = store.Now()
	if err := s.saveAvatar(site.ID, r.MultipartForm); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.Store.SaveSite(site); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.render(r.Context(), site.ID)
	writeJSON(w, 200, site)
}

func formValue(r *http.Request, key string) (string, bool) {
	if r.MultipartForm != nil {
		if v, ok := r.MultipartForm.Value[key]; ok && len(v) > 0 {
			return v[0], true
		}
		return "", false
	}
	v, ok := r.Form[key]
	if !ok || len(v) == 0 {
		return "", false
	}
	return v[0], true
}

// saveAvatar stores an uploaded image as avatar.png (re-encoded to PNG).
func (s *Server) saveAvatar(siteID string, form *multipart.Form) error {
	if form == nil || len(form.File["avatar"]) == 0 {
		return nil
	}
	fh := form.File["avatar"][0]
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("avatar must be a PNG, JPEG, or GIF image")
	}
	out, err := os.Create(filepath.Join(s.Store.SiteDir(siteID), "avatar.png"))
	if err != nil {
		return err
	}
	defer out.Close()
	return png.Encode(out, img)
}

func (s *Server) createPost(w http.ResponseWriter, r *http.Request) {
	site, ok := s.site(w, r)
	if !ok {
		return
	}
	if site.IsArchived() {
		writeErr(w, 400, errors.New("Planet is archived."))
		return
	}
	if err := r.ParseMultipartForm(attachmentLimit); err != nil {
		writeErr(w, 400, err)
		return
	}
	id := newUUID()
	created := store.Now()
	if d := r.FormValue("date"); d != "" {
		t, err := time.Parse(time.RFC3339, d)
		if err != nil {
			writeErr(w, 400, fmt.Errorf("date must be ISO 8601: %w", err))
			return
		}
		created = store.FromTime(t)
	}
	empty := ""
	post := &store.Post{
		ID: id, Title: r.FormValue("title"), Content: r.FormValue("content"), Created: created,
		ArticleType: 0, Link: "/" + id + "/", Attachments: []string{}, CIDs: map[string]string{},
		Tags: parseTags(r.FormValue("tags")), Summary: &empty, Slug: &empty,
	}
	if err := s.saveAttachments(site.ID, post, r.MultipartForm, "append"); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.savePostAndRender(w, r, site.ID, post)
}

func (s *Server) modifyPost(w http.ResponseWriter, r *http.Request) {
	site, post, ok := s.post(w, r)
	if !ok {
		return
	}
	if site.IsArchived() {
		writeErr(w, 400, errors.New("Planet is archived."))
		return
	}
	if err := r.ParseMultipartForm(attachmentLimit); err != nil {
		writeErr(w, 400, err)
		return
	}
	if v, ok := formValue(r, "title"); ok {
		post.Title = v
	}
	if v, ok := formValue(r, "content"); ok {
		post.Content = v
	}
	if v, ok := formValue(r, "tags"); ok {
		post.Tags = parseTags(v)
	}
	if v, ok := formValue(r, "slug"); ok {
		v = strings.Trim(strings.TrimSpace(v), "/")
		post.Slug = &v
		post.Link = "/" + post.Dir() + "/"
	}
	if v, ok := formValue(r, "externalLink"); ok {
		post.ExternalLink = &v
	}
	if v, ok := formValue(r, "date"); ok && v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeErr(w, 400, fmt.Errorf("date must be ISO 8601: %w", err))
			return
		}
		post.Created = store.FromTime(t)
	}
	mode := r.URL.Query().Get("attachmentMode")
	if mode == "" {
		mode = "keep"
	}
	if err := s.saveAttachments(site.ID, post, r.MultipartForm, mode); err != nil {
		writeErr(w, 500, err)
		return
	}
	now := store.Now()
	post.Modified = &now
	// generated files depend on title, content, and attachments; rebuild them
	for _, f := range []string{"nft.json", "nft.json.cid.txt", "_cover.png"} {
		os.Remove(filepath.Join(s.Store.PublicDir(site.ID), post.ID, f))
	}
	delete(post.CIDs, "_cover.png")
	s.savePostAndRender(w, r, site.ID, post)
}

func parseTags(s string) map[string]string {
	out := map[string]string{}
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			out[t] = t
		}
	}
	return out
}

// saveAttachments writes uploaded files into the post's source folder.
// mode: keep (ignore uploads unless present), append, replace.
func (s *Server) saveAttachments(siteID string, post *store.Post, form *multipart.Form, mode string) error {
	if form == nil {
		return nil
	}
	files := form.File["attachments"]
	if len(files) == 0 {
		files = form.File["attachment"]
	}
	if len(files) == 0 && mode != "replace" {
		return nil
	}
	dir := s.Store.PostDir(siteID, post.ID)
	if mode == "replace" {
		os.RemoveAll(dir)
		os.RemoveAll(filepath.Join(s.Store.PublicDir(siteID), post.ID))
		post.Attachments = []string{}
		post.CIDs = map[string]string{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, fh := range files {
		name := filepath.Base(fh.Filename)
		if name == "" || name == "." || strings.HasPrefix(name, "_") {
			continue
		}
		src, err := fh.Open()
		if err != nil {
			return err
		}
		dst, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			src.Close()
			return err
		}
		_, err = io.Copy(dst, src)
		src.Close()
		dst.Close()
		if err != nil {
			return err
		}
		os.Remove(filepath.Join(s.Store.PublicDir(siteID), post.ID, name)) // force a fresh copy and CID
		delete(post.CIDs, name)
		found := false
		for _, a := range post.Attachments {
			if a == name {
				found = true
			}
		}
		if !found {
			post.Attachments = append(post.Attachments, name)
		}
	}
	sort.Strings(post.Attachments)
	// a fresh cover is generated for text-only posts
	if post.TextOnly() {
		kept := post.Attachments[:0]
		for _, a := range post.Attachments {
			if a != "_cover.png" {
				kept = append(kept, a)
			}
		}
		post.Attachments = kept
	}
	return nil
}

func (s *Server) savePostAndRender(w http.ResponseWriter, r *http.Request, siteID string, post *store.Post) {
	if err := s.Store.SavePost(siteID, post); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.render(r.Context(), siteID)
	post, _ = s.Store.Post(siteID, post.ID) // render may fill cids and dimensions
	writeJSON(w, 200, post)
}

func (s *Server) publishSite(w http.ResponseWriter, r *http.Request, force bool) {
	site, ok := s.site(w, r)
	if !ok {
		return
	}
	if site.IsArchived() {
		writeErr(w, 400, errors.New("Planet is archived."))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute) // outlives a closed browser tab
	defer cancel()
	res, err := s.Pub.Publish(ctx, site.ID, force)
	if err != nil {
		if errors.Is(err, publish.ErrPublishedElsewhere) || errors.Is(err, publish.ErrWouldResetSequence) {
			writeErr(w, 409, err)
			return
		}
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"cid": res.CID, "sequence": res.Sequence, "ipns": site.IPNS})
}

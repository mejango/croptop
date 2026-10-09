package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/collaboration"
	"github.com/mejango/croptop/internal/store"
)

func (s *Server) collaborationManager() *collaboration.Manager {
	s.collabOnce.Do(func() { s.collab = &collaboration.Manager{Store: s.Store, Node: s.Node} })
	return s.collab
}
func (s *Server) resolveSources(ctx context.Context, names []string) ([]store.Contributor, error) {
	if len(names) == 0 || len(names) > 50 {
		return nil, errors.New("enter between 1 and 50 site addresses")
	}
	if s.Follow == nil {
		return nil, errors.New("source connection unavailable")
	}
	out := []store.Contributor{}
	seen := map[string]bool{}
	for _, name := range names {
		clean := cleanCollaborationName(name)
		if clean == "" {
			return nil, errors.New("use an ENS name or IPNS address for each site")
		}
		entry, e := s.Follow.Follow(ctx, clean)
		if e != nil {
			return nil, e
		}
		if !seen[entry.IPNS] {
			out = append(out, store.Contributor{IPNS: entry.IPNS, Name: entry.Title, Mode: "all"})
			seen[entry.IPNS] = true
		}
	}
	return out, nil
}
func (s *Server) routesCollaboration(mux *http.ServeMux) {
	mux.HandleFunc("GET /v0/croptop/capabilities", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"composites": true}) })
	base := "/v0/croptop/sites/{id}/collaboration"
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		st, e := s.collaborationManager().State(site.ID)
		if e != nil {
			writeErr(w, 500, e)
			return
		}
		writeJSON(w, 200, st)
	})
	mux.HandleFunc("POST "+base+"/sources", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		var in struct {
			Names []string `json:"names"`
		}
		if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); e != nil {
			writeErr(w, 400, e)
			return
		}
		cs, e := s.resolveSources(r.Context(), in.Names)
		if e != nil {
			writeErr(w, 400, e)
			return
		}
		if e = s.collaborationManager().AddSources(site.ID, cs); e != nil {
			writeErr(w, 400, e)
			return
		}
		st, e := s.syncComposite(r.Context(), site.ID)
		if e != nil {
			writeErr(w, 500, e)
			return
		}
		writeJSON(w, 200, st)
	})
	mux.HandleFunc("POST "+base+"/refresh", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		st, e := s.syncComposite(r.Context(), site.ID)
		if e != nil {
			writeErr(w, 500, e)
			return
		}
		writeJSON(w, 200, st)
	})
	mux.HandleFunc("GET "+base+"/sources/{ipns}/avatar", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		name := r.PathValue("ipns")
		if !collaboration.ValidName(name) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		http.ServeFile(w, r, filepath.Join(s.Store.SiteDir(site.ID), "collaboration", "avatars", name+".png"))
	})
	// Keep old clients from creating a workflow that no longer exists.
	for _, route := range []string{"POST " + base + "/contributors", "DELETE " + base + "/contributors/{ipns}", "POST " + base + "/review/{item}", "POST /v0/croptop/sites/{id}/posts/{post}/submit"} {
		mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, 410, errors.New("sites now merge automatically; update Croptop to use Curate"))
		})
	}
}
func (s *Server) syncComposite(ctx context.Context, id string) (collaboration.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, e := s.collaborationManager().Sync(ctx, id)
	if e == nil && s.Pub != nil {
		e = s.Pub.Render.Render(ctx, id)
	}
	return st, e
}

// Composites update locally in the background. Publishing remains the site's
// normal action, which also refreshes sources before publishing.
func (s *Server) RunComposites(ctx context.Context) {
	run := func() {
		sites, e := s.Store.Sites()
		if e != nil {
			return
		}
		for _, site := range sites {
			if site.IsArchived() || len(collaboration.Sources(site)) == 0 {
				continue
			}
			c, cancel := context.WithTimeout(ctx, 10*time.Minute)
			_, e = s.syncComposite(c, site.ID)
			cancel()
			if e != nil {
				s.log("merge %s: %v", site.Name, e)
			}
		}
	}
	run()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
func cleanCollaborationName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "ipns://")
	s = strings.TrimPrefix(s, "/ipns/")
	s = strings.TrimSuffix(s, "/")
	if collaboration.ValidName(s) {
		return s
	}
	if strings.HasSuffix(s, ".eth") && len(s) <= 255 && !strings.ContainsAny(s, "/\\?# \t\n\r") {
		return s
	}
	return ""
}

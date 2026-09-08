package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/follow"
)

// routesFollow: following other sites, and the hosts view for owned sites.
func (s *Server) routesFollow(mux *http.ServeMux) {
	mux.HandleFunc("GET /v0/croptop/following", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Follow.List()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		if list == nil {
			list = []*follow.Entry{}
		}
		writeJSON(w, 200, list)
	})
	mux.HandleFunc("POST /v0/croptop/following", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, 400, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		e, err := s.Follow.Follow(ctx, in.Name)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, e)
	})
	mux.HandleFunc("DELETE /v0/croptop/following/{ipns}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Follow.Unfollow(r.PathValue("ipns")); err != nil {
			writeErr(w, 404, err)
			return
		}
		writeJSON(w, 200, map[string]string{"unfollowed": r.PathValue("ipns")})
	})
	mux.HandleFunc("POST /v0/croptop/following/{ipns}/refresh", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		changed, err := s.Follow.Refresh(ctx, r.PathValue("ipns"))
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		e, _ := s.Follow.Get(r.PathValue("ipns"))
		writeJSON(w, 200, map[string]any{"changed": changed, "entry": e})
	})
	// followed site trees, open like owned public trees
	mux.HandleFunc("GET /f/{ipns}/{path...}", func(w http.ResponseWriter, r *http.Request) {
		ipns := r.PathValue("ipns")
		if _, err := s.Follow.Get(ipns); err != nil {
			http.NotFound(w, r)
			return
		}
		p := r.PathValue("path")
		if p == "" || strings.HasSuffix(p, "/") {
			p += "index.html"
		}
		http.ServeFile(w, r, s.Follow.SiteDir(ipns)+"/"+p)
	})
	mux.HandleFunc("GET /f/{ipns}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/f/"+r.PathValue("ipns")+"/", http.StatusFound)
	})

	mux.HandleFunc("GET /v0/croptop/sites/{id}/hosts", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		if site.LastPublishedCID == nil {
			writeErr(w, 400, errors.New("site has not been published"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		peers, err := s.Node.FindProviders(ctx, *site.LastPublishedCID)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		info, _ := s.Node.Info(ctx)
		if peers == nil {
			peers = []string{}
		}
		writeJSON(w, 200, map[string]any{"cid": *site.LastPublishedCID, "count": len(peers), "peers": peers, "self": info.PeerID})
	})
}

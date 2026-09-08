package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/mejango/croptop/internal/tpl"
)

// routesTemplate: installed templates, per-site fork and editing.
func (s *Server) routesTemplate(mux *http.ServeMux) {
	mux.HandleFunc("GET /v0/croptop/templates", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Tpl.Installed()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, list)
	})
	mux.HandleFunc("POST /v0/croptop/templates", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, 400, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		info, err := s.Tpl.Install(ctx, in.Name)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, info)
	})

	mux.HandleFunc("GET /v0/croptop/sites/{id}/template", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		_, source, err := s.Tpl.For(site)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		files, _ := s.Tpl.Files(site)
		if files == nil {
			files = []string{}
		}
		editable := []string{}
		for _, f := range files {
			if tpl.Editable(f) {
				editable = append(editable, f)
			}
		}
		writeJSON(w, 200, map[string]any{"choice": tpl.ChoiceOf(site), "source": source, "files": files, "editable": editable})
	})
	mux.HandleFunc("PUT /v0/croptop/sites/{id}/template", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		var in struct{ CID string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := s.Tpl.Use(site.ID, in.CID); err != nil {
			writeErr(w, 400, err)
			return
		}
		s.render(r.Context(), site.ID)
		writeJSON(w, 200, map[string]string{"cid": in.CID})
	})
	mux.HandleFunc("POST /v0/croptop/sites/{id}/template/fork", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		if err := s.Tpl.Fork(site.ID); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"forked": true})
	})
	mux.HandleFunc("POST /v0/croptop/sites/{id}/template/reset", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		if err := s.Tpl.Reset(site.ID); err != nil {
			writeErr(w, 400, err)
			return
		}
		s.render(r.Context(), site.ID)
		writeJSON(w, 200, map[string]bool{"forked": false})
	})
	mux.HandleFunc("GET /v0/croptop/sites/{id}/template/file", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		b, err := s.Tpl.ReadFile(site, r.URL.Query().Get("path"))
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(b)
	})
	mux.HandleFunc("PUT /v0/croptop/sites/{id}/template/file", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := s.Tpl.WriteFile(site, r.URL.Query().Get("path"), b); err != nil {
			writeErr(w, 400, err)
			return
		}
		s.render(r.Context(), site.ID)
		writeJSON(w, 200, map[string]string{"saved": r.URL.Query().Get("path")})
	})
	mux.HandleFunc("GET /v0/croptop/sites/{id}/template/upstream", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		st, err := s.Tpl.Upstream(ctx, site)
		if err != nil && st.Upstream != "" {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, st)
	})
	mux.HandleFunc("POST /v0/croptop/sites/{id}/template/upstream/take", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		p := r.URL.Query().Get("path")
		if p == "" {
			writeErr(w, 400, errors.New("path is required"))
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := s.Tpl.TakeTheirs(ctx, site, p); err != nil {
			writeErr(w, 400, err)
			return
		}
		s.render(r.Context(), site.ID)
		writeJSON(w, 200, map[string]string{"took": p})
	})
}

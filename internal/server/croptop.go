package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/gateway"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/internal/update"
)

// routesCroptop are the UI's own routes, beyond Planet's API.
func (s *Server) routesCroptop(mux *http.ServeMux) {
	mux.HandleFunc("GET /v0/croptop/status", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		info, err := s.Node.Info(ctx)
		latest := s.latestRelease()
		writeJSON(w, 200, map[string]any{
			"version":  s.Version,
			"latest":   latest,
			"update":   update.Newer(s.Version, latest),
			"dataDir":  s.DataDir,
			"listen":   s.Cfg.Listen,
			"passcode": s.Cfg.HasPasscode(),
			"ipfs": map[string]any{
				"running": s.Node.Running() && err == nil, "peers": info.Peers, "peerID": info.PeerID,
				"version": info.Version, "lastError": s.Node.LastError(),
			},
		})
	})
	mux.HandleFunc("POST /v0/croptop/update", func(w http.ResponseWriter, r *http.Request) {
		rel, err := update.Latest(r.Context())
		if err != nil {
			writeErr(w, 502, err)
			return
		}
		if !update.Newer(s.Version, rel.Version()) {
			writeErr(w, 400, fmt.Errorf("croptop %s is already the newest release", s.Version))
			return
		}
		exe, err := update.Apply(r.Context(), rel, func(m string) { s.log("%s", m) })
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"installed": rel.Version(), "restarting": true})
		go func() {
			time.Sleep(500 * time.Millisecond) // let the response leave
			s.log("restarting as croptop %s", rel.Version())
			if err := update.Restart(exe); err != nil {
				s.log("restart failed: %v (start croptop again by hand)", err)
			}
		}()
	})
	mux.HandleFunc("POST /v0/croptop/quit", func(w http.ResponseWriter, r *http.Request) {
		if s.Quit == nil {
			writeErr(w, 400, fmt.Errorf("this console cannot quit itself"))
			return
		}
		writeJSON(w, 200, map[string]any{"quitting": true})
		go func() { time.Sleep(300 * time.Millisecond); s.Quit() }()
	})
	mux.HandleFunc("POST /v0/croptop/markdown", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(render.Markdown(string(b))))
	})
	mux.HandleFunc("GET /v0/croptop/gateways", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, gateway.Table)
	})
	mux.HandleFunc("GET /v0/croptop/template", func(w http.ResponseWriter, r *http.Request) {
		meta, err := s.meta()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, meta)
	})
	mux.HandleFunc("GET /v0/croptop/sites/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		stored, err := s.Store.TemplateSettings(site.ID)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		meta, err := s.metaFor(site)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, meta.SettingsWithDefaults(stored))
	})
	mux.HandleFunc("PUT /v0/croptop/sites/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		var m map[string]any
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := s.Store.SaveTemplateSettings(site.ID, m); err != nil {
			writeErr(w, 500, err)
			return
		}
		s.render(r.Context(), site.ID)
		writeJSON(w, 200, m)
	})
	mux.HandleFunc("PUT /v0/croptop/sites/{id}", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		var in struct {
			Name, About *string
			Domain      *string
			Tags        map[string]string
			Archived    *bool
			Gateway     *string
			Host        *string                    // croptop host base URL to push to, "" turns pushing off
			Custom      map[string]json.RawMessage // customCodeHead etc, passed through to Raw
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, 400, err)
			return
		}
		if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
			site.Name = *in.Name
		}
		if in.About != nil {
			site.About = *in.About
		}
		if in.Domain != nil {
			d := strings.TrimSpace(*in.Domain)
			site.Domain = &d
		}
		if in.Tags != nil {
			site.Tags = in.Tags
		}
		if in.Archived != nil {
			site.Archived = in.Archived
		}
		if in.Gateway != nil {
			gateway.Set(site, *in.Gateway)
		}
		if in.Host != nil {
			publish.SetHost(site, *in.Host)
		}
		if site.Raw == nil {
			site.Raw = map[string]json.RawMessage{}
		}
		for k, v := range in.Custom {
			if strings.HasPrefix(k, "customCode") || k == "doNotIndex" || strings.HasSuffix(k, "Username") || k == "discordLink" {
				site.Raw[k] = v
			}
		}
		site.Updated = store.Now()
		if err := s.Store.SaveSite(site); err != nil {
			writeErr(w, 500, err)
			return
		}
		s.render(r.Context(), site.ID)
		writeJSON(w, 200, site)
	})
	mux.HandleFunc("POST /v0/croptop/sites/{id}/name", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		var in struct{ Name string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := s.Pub.Claim(r.Context(), site, in.Name); err != nil {
			writeErr(w, 400, err)
			return
		}
		s.render(r.Context(), site.ID)
		writeJSON(w, 200, site)
	})
	mux.HandleFunc("GET /v0/croptop/sites/{id}/key", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		pem, err := s.Node.Keystore().ExportPEM(site.ID)
		if err != nil {
			writeErr(w, 404, errors.New("no key for this site on this machine"))
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Write(pem)
	})
	mux.HandleFunc("POST /v0/croptop/sites/{id}/key", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		var in struct{ PEM string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, 400, err)
			return
		}
		ks := s.Node.Keystore()
		if err := ks.ImportPEM(site.ID, []byte(in.PEM)); err != nil {
			writeErr(w, 400, err)
			return
		}
		if name, _ := ks.Name(site.ID); name != site.IPNS {
			ks.Delete(site.ID)
			writeErr(w, 400, errors.New("that key does not belong to this site"))
			return
		}
		writeJSON(w, 200, map[string]string{"ipns": site.IPNS})
	})
	mux.HandleFunc("POST /v0/croptop/adopt", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name, PEM string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, 400, err)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		id, err := s.Pub.Adopt(ctx, in.Name, []byte(in.PEM))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		s.Pub.Render.Render(ctx, id)
		writeJSON(w, 200, map[string]string{"id": id})
	})
	mux.HandleFunc("POST /v0/croptop/sites/{id}/sync", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		res, err := s.Pub.Sync(ctx, site.ID)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, res)
	})
	mux.HandleFunc("POST /v0/croptop/sites/{id}/publish", func(w http.ResponseWriter, r *http.Request) {
		s.publishSite(w, r, r.URL.Query().Get("force") == "true")
	})
	mux.HandleFunc("GET /v0/croptop/sites/{id}/url", func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.site(w, r)
		if !ok {
			return
		}
		writeJSON(w, 200, map[string]string{"url": render.SiteURL(site)})
	})
}

// latestRelease is the newest release tag, checked at most hourly.
func (s *Server) latestRelease() string {
	s.relMu.Lock()
	defer s.relMu.Unlock()
	if time.Since(s.relAt) < time.Hour {
		return s.rel
	}
	s.relAt = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if r, err := update.Latest(ctx); err == nil {
		s.rel = r.Version()
	}
	return s.rel
}

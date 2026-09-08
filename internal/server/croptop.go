package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/gateway"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

// routesCroptop are the UI's own routes, beyond Planet's API.
func (s *Server) routesCroptop(mux *http.ServeMux) {
	mux.HandleFunc("GET /v0/croptop/status", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		info, err := s.Node.Info(ctx)
		writeJSON(w, 200, map[string]any{
			"version":  s.Version,
			"dataDir":  s.DataDir,
			"listen":   s.Cfg.Listen,
			"passcode": s.Cfg.HasPasscode(),
			"ipfs": map[string]any{
				"running": s.Node.Running() && err == nil, "peers": info.Peers, "peerID": info.PeerID,
				"version": info.Version, "gateway": s.Node.GatewayURL(), "lastError": s.Node.LastStderr(),
			},
		})
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
		meta, err := s.meta()
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

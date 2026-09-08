// Package server is the local HTTP surface: the Planet-compatible /v0 API,
// the rendered sites at /<site-uuid>/, and the embedded UI at /.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/mejango/croptop/internal/config"
	"github.com/mejango/croptop/internal/follow"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

type Server struct {
	Store     *store.Store
	Pub       *publish.Publisher
	Follow    *follow.Store
	Node      ipfs.Engine
	Cfg       *config.Config
	UI        fs.FS
	Templates fs.FS
	Version   string
	DataDir   string
	Log       func(string)

	mu sync.Mutex // serializes render/publish per process
}

var uuidRe = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routesAPI(mux)
	s.routesCroptop(mux)
	s.routesFollow(mux)

	// UI
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, s.UI, "index.html")
	})
	for _, f := range []string{"app.js", "style.css"} {
		f := f
		mux.HandleFunc("GET /"+f, func(w http.ResponseWriter, r *http.Request) {
			http.ServeFileFS(w, r, s.UI, f)
		})
	}
	// the template's own web fonts, so the console looks like the site
	mux.HandleFunc("GET /fonts/{file}", func(w http.ResponseWriter, r *http.Request) {
		f := r.PathValue("file")
		if !strings.HasSuffix(f, ".woff2") || strings.Contains(f, "/") {
			http.NotFound(w, r)
			return
		}
		http.ServeFileFS(w, r, s.Templates, "assets/"+f)
	})
	// public trees: /<site-uuid>/...
	mux.HandleFunc("GET /{site}/{path...}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("site")
		if !uuidRe.MatchString(id) {
			http.NotFound(w, r)
			return
		}
		p := r.PathValue("path")
		if p == "" || strings.HasSuffix(p, "/") {
			p += "index.html"
		}
		http.ServeFile(w, r, s.Store.PublicDir(strings.ToUpper(id))+"/"+p)
	})
	mux.HandleFunc("GET /{site}", func(w http.ResponseWriter, r *http.Request) {
		if uuidRe.MatchString(r.PathValue("site")) {
			http.Redirect(w, r, "/"+r.PathValue("site")+"/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	return s.auth(mux)
}

// auth requires HTTP Basic (user "Croptop") when a passcode is configured,
// except for the public site trees. Planet's own clients (pn) speak this.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Cfg.HasPasscode() || isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if ok && subtle.ConstantTimeCompare([]byte(strings.ToLower(user)), []byte("croptop")) == 1 && s.Cfg.CheckPasscode(pass) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="Croptop"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func isPublicPath(p string) bool {
	seg := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 2)[0]
	if uuidRe.MatchString(seg) || strings.HasPrefix(p, "/f/") {
		return true
	}
	return strings.HasPrefix(p, "/v0/planets/my/") && strings.HasSuffix(p, "/public")
}

// ListenAndServe refuses non-loopback binds without a passcode.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) && !s.Cfg.HasPasscode() {
		return errors.New("refusing to listen on a non-loopback address without a passcode; run `croptop passcode set` first")
	}
	srv := &http.Server{Addr: addr, Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) log(format string, a ...any) {
	if s.Log != nil {
		s.Log(fmt.Sprintf(format, a...))
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// render re-renders a site after a change; errors are logged, not fatal,
// so an editing mistake never loses the saved data.
func (s *Server) render(ctx context.Context, siteID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Pub.Render.Render(ctx, siteID); err != nil {
		s.log("render %s: %v", siteID, err)
	}
}

func (s *Server) meta() (*render.Meta, error) { return render.LoadMeta(s.Templates) }

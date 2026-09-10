package server

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Quick posts: `croptop shot` captures the screen and parks the image here,
// then opens the console on a small form that turns it into a post.
func (s *Server) quickRoutes(mux *http.ServeMux) {
	dir := filepath.Join(s.DataDir, "quick")
	mux.HandleFunc("POST /v0/croptop/quick", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			writeErr(w, 400, err)
			return
		}
		f, hdr, err := r.FormFile("image")
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		defer f.Close()
		var b [8]byte
		rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		ext := strings.ToLower(filepath.Ext(hdr.Filename))
		if ext == "" {
			ext = ".png"
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			writeErr(w, 500, err)
			return
		}
		out, err := os.Create(filepath.Join(dir, id+ext))
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		_, err = io.Copy(out, f)
		out.Close()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"id": id + ext, "url": "/v0/croptop/quick/" + id + ext, "open": "/#/quick/" + id + ext})
	})
	mux.HandleFunc("GET /v0/croptop/quick/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := filepath.Base(r.PathValue("id"))
		http.ServeFile(w, r, filepath.Join(dir, id))
	})
	mux.HandleFunc("DELETE /v0/croptop/quick/{id}", func(w http.ResponseWriter, r *http.Request) {
		os.Remove(filepath.Join(dir, filepath.Base(r.PathValue("id"))))
		w.WriteHeader(204)
	})
}

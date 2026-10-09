package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var errNoFiles = errors.New("no files: send them as multipart fields named files")

// Quick posts: `croptop post <files>` (or files dropped on the app) park media
// here as one group, then the console opens a small form that turns the group
// into a post.
func (s *Server) quickRoutes(mux *http.ServeMux) {
	dir := filepath.Join(s.DataDir, "quick")
	mux.HandleFunc("POST /v0/croptop/quick", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(512 << 20); err != nil {
			writeErr(w, 400, err)
			return
		}
		files := append(r.MultipartForm.File["files"], r.MultipartForm.File["image"]...)
		if len(files) == 0 {
			writeErr(w, 400, errNoFiles)
			return
		}
		var b [8]byte
		rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		if err := os.MkdirAll(filepath.Join(dir, id), 0o755); err != nil {
			writeErr(w, 500, err)
			return
		}
		var names []string
		for _, hdr := range files {
			name := filepath.Base(hdr.Filename)
			if name == "" || name == "." {
				name = "file"
			}
			f, err := hdr.Open()
			if err != nil {
				writeErr(w, 400, err)
				return
			}
			out, err := os.Create(filepath.Join(dir, id, name))
			if err == nil {
				_, err = io.Copy(out, f)
				out.Close()
			}
			f.Close()
			if err != nil {
				writeErr(w, 500, err)
				return
			}
			names = append(names, name)
		}
		writeJSON(w, 200, map[string]any{"id": id, "files": names, "open": "/#/quick/" + id})
	})
	mux.HandleFunc("GET /v0/croptop/quick/{id}", func(w http.ResponseWriter, r *http.Request) {
		entries, err := os.ReadDir(filepath.Join(dir, filepath.Base(r.PathValue("id"))))
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		names := []string{}
		for _, e := range entries {
			if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		writeJSON(w, 200, map[string]any{"id": filepath.Base(r.PathValue("id")), "files": names})
	})
	mux.HandleFunc("GET /v0/croptop/quick/{id}/{name}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(dir, filepath.Base(r.PathValue("id")), filepath.Base(r.PathValue("name"))))
	})
	mux.HandleFunc("DELETE /v0/croptop/quick/{id}", func(w http.ResponseWriter, r *http.Request) {
		os.RemoveAll(filepath.Join(dir, filepath.Base(r.PathValue("id"))))
		w.WriteHeader(204)
	})
}

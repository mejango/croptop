package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mejango/croptop/internal/gateway"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/follow"
)

// routesFollow: following other sites, and the hosts view for owned sites.
func (s *Server) routesFollow(mux *http.ServeMux) {
	// The feed: every feed post from every followed site, newest first, read
	// from the copies this node keeps. Pages stay out, as on the sites themselves.
	mux.HandleFunc("GET /v0/croptop/feed", func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 500 {
			limit = n
		}
		entries, _ := s.Follow.List()
		type item struct {
			IPNS    string          `json:"ipns"`
			Site    string          `json:"site"`
			ID      string          `json:"id"`
			Title   string          `json:"title"`
			Summary string          `json:"summary"`
			Link    string          `json:"link"`
			URL     string          `json:"url"`
			Created store.AppleTime `json:"created"`
			Preview bool            `json:"preview"`
			Pinned  bool            `json:"pinned"`
			Hero    string          `json:"hero,omitempty"` // an image to show with the item, served under Link
		}
		items := []item{}
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(s.Follow.SiteDir(e.IPNS), "planet.json"))
			if err != nil {
				continue
			}
			var pub struct {
				Name     string              `json:"name"`
				Articles []render.PublicPost `json:"articles"`
			}
			if json.Unmarshal(b, &pub) != nil {
				continue
			}
			site := pub.Name
			if site == "" {
				site = e.Title
			}
			base := "https://" + e.IPNS + "." + gateway.Get("crop.top").Domain + "/"
			if strings.HasSuffix(e.Name, ".eth") {
				base = "https://" + strings.TrimSuffix(e.Name, ".eth") + "." + gateway.Get("crop.top").Domain + "/"
			}
			for _, a := range pub.Articles {
				if a.ArticleType != 0 {
					continue
				}
				items = append(items, item{
					IPNS: e.IPNS, Site: site, ID: a.ID, Title: strings.TrimSpace(a.Title), Summary: plainText(a.Content, 240),
					Link: "/f/" + e.IPNS + "/" + a.ID + "/", URL: base + a.ID + "/", Created: a.Created,
					Preview: hasPreview(a), Pinned: a.Pinned != nil, Hero: heroOf(a),
				})
			}
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].Created > items[j].Created })
		if len(items) > limit {
			items = items[:limit]
		}
		writeJSON(w, 200, items)
	})
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
		// the DHT does not list this node among providers of its own content
		if _, err := os.Stat(s.Store.PublicDir(site.ID)); err == nil && info.PeerID != "" {
			found := false
			for _, p := range peers {
				if p == info.PeerID {
					found = true
				}
			}
			if !found {
				peers = append([]string{info.PeerID}, peers...)
			}
		}
		writeJSON(w, 200, map[string]any{"cid": *site.LastPublishedCID, "count": len(peers), "peers": peers, "self": info.PeerID})
	})
}

var (
	tagRe      = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>|<[^>]+>`)
	mdRe       = regexp.MustCompile("[#*_`>]+|!?\\[([^\\]]*)\\]\\([^)]*\\)")
	spaceRe    = regexp.MustCompile(`\s+`)
	punctRe    = regexp.MustCompile(`\s+([.,;:!?])`)
	previewTag = regexp.MustCompile(`<script[^>]+type=["']croptop/preview["']`)
)

// plainText is the first n characters of a post's content without markup.
func plainText(content string, n int) string {
	t := tagRe.ReplaceAllString(content, " ")
	t = mdRe.ReplaceAllString(t, "$1")
	t = strings.TrimSpace(spaceRe.ReplaceAllString(t, " "))
	t = punctRe.ReplaceAllString(t, "$1")
	if len(t) > n {
		if i := strings.LastIndex(t[:n], " "); i > n/2 {
			t = t[:i]
		} else {
			t = t[:n]
		}
		t += "…"
	}
	return t
}

// hasPreview mirrors croptop.hasPreview in the template.
func hasPreview(a render.PublicPost) bool {
	for _, att := range a.Attachments {
		if att == "preview.js" {
			return true
		}
	}
	return previewTag.MatchString(a.Content)
}

// heroOf is the post's hero image, or its first image attachment.
func heroOf(a render.PublicPost) string {
	if a.HeroImageFilename != nil && *a.HeroImageFilename != "" {
		return *a.HeroImageFilename
	}
	if a.HeroImage != nil && *a.HeroImage != "" { // published planet.json carries it as a URL
		return path.Base(*a.HeroImage)
	}
	for _, name := range a.Attachments {
		switch strings.ToLower(filepath.Ext(name)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp":
			return name
		}
	}
	return ""
}

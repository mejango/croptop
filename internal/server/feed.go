package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mejango/croptop/internal/gateway"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

type feedItem struct {
	SiteID      string          `json:"siteID,omitempty"`
	IPNS        string          `json:"ipns"`
	Site        string          `json:"site"`
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	Content     string          `json:"content"`
	Attachments []string        `json:"attachments"`
	Link        string          `json:"link"`
	URL         string          `json:"url"`
	Created     store.AppleTime `json:"created"`
	Preview     bool            `json:"preview"`
	Pinned      bool            `json:"pinned"`
	Hero        string          `json:"hero,omitempty"` // served under Link
}

func feedPost(a render.PublicPost) feedItem {
	attachments := a.Attachments
	if attachments == nil {
		attachments = []string{}
	}
	return feedItem{
		ID: a.ID, Title: strings.TrimSpace(a.Title), Summary: plainText(a.Content, 240),
		Content: a.Content, Attachments: attachments, Created: a.Created,
		Preview: hasPreview(a), Pinned: a.Pinned != nil, Hero: heroOf(a),
	}
}

// feed reads owned posts or cached followed posts without fetching the network.
// A followed site's filter is applied before sorting and limiting the result.
func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	source, ipns := query.Get("source"), strings.TrimSpace(query.Get("ipns"))
	if source == "" {
		source = "following"
	}
	if source != "following" && source != "owned" {
		writeErr(w, 400, errors.New("source must be following or owned"))
		return
	}
	if source == "owned" && ipns != "" {
		writeErr(w, 400, errors.New("ipns filtering is only available for followed sites"))
		return
	}
	offset := 0
	if raw := query.Get("offset"); raw != "" {
		var err error
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			writeErr(w, 400, errors.New("offset must be a nonnegative integer"))
			return
		}
	}
	limit := 50
	if n, err := strconv.Atoi(query.Get("limit")); err == nil && n > 0 && n <= 500 {
		limit = n
	}
	items := []feedItem{}
	if source == "owned" {
		sites, err := s.Store.Sites()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		for _, site := range sites {
			if site.IsArchived() {
				continue
			}
			posts, err := s.Store.Posts(site.ID)
			if err != nil {
				writeErr(w, 500, fmt.Errorf("could not read posts from %s: %w", site.Name, err))
				return
			}
			for _, post := range posts {
				if post.ArticleType != 0 {
					continue
				}
				item := feedPost(render.NewPublicPost(site, post))
				item.SiteID, item.IPNS, item.Site = site.ID, site.IPNS, site.Name
				item.Link = "/" + site.ID + "/" + post.ID + "/"
				item.URL = render.BrowserURL(site, post)
				items = append(items, item)
			}
		}
	} else {
		entries, err := s.Follow.List()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		matched := ipns == ""
		for _, entry := range entries {
			if ipns != "" && entry.IPNS != ipns {
				continue
			}
			matched = true
			var pub *struct {
				Name     string              `json:"name"`
				Articles []render.PublicPost `json:"articles"`
			}
			data, err := os.ReadFile(filepath.Join(s.Follow.SiteDir(entry.IPNS), "planet.json"))
			if err != nil || json.Unmarshal(data, &pub) != nil || pub == nil {
				if ipns != "" {
					writeErr(w, 503, errors.New("this site's saved content is unavailable; refresh the site and try again"))
					return
				}
				continue
			}
			name := pub.Name
			if name == "" {
				name = entry.Title
			}
			if name == "" {
				name = entry.Name
			}
			base := "https://" + entry.IPNS + "." + gateway.Get("crop.top").Domain + "/"
			if strings.HasSuffix(entry.Name, ".eth") {
				base = "https://" + strings.TrimSuffix(entry.Name, ".eth") + "." + gateway.Get("crop.top").Domain + "/"
			}
			for _, post := range pub.Articles {
				if post.ArticleType != 0 {
					continue
				}
				item := feedPost(post)
				item.IPNS, item.Site = entry.IPNS, name
				item.Link, item.URL = "/f/"+entry.IPNS+"/"+post.ID+"/", base+post.ID+"/"
				items = append(items, item)
			}
		}
		if !matched {
			writeErr(w, 404, errors.New("this site is not in Following"))
			return
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Created > items[j].Created })
	if offset >= len(items) {
		items = items[:0]
	} else {
		items = items[offset:]
	}
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, 200, items)
}

// Package store reads and writes the site library on disk in the same
// JSON shapes the Planet/Croptop Mac app uses.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Store struct {
	Root string // data dir; sites live under Root/sites, rendered output under Root/public
}

var ErrNotFound = errors.New("not found")

func (s *Store) SitesDir() string          { return filepath.Join(s.Root, "sites") }
func (s *Store) SiteDir(id string) string   { return filepath.Join(s.SitesDir(), id) }
func (s *Store) PublicDir(id string) string { return filepath.Join(s.Root, "public", id) }
func (s *Store) ArticlesDir(id string) string {
	return filepath.Join(s.SiteDir(id), "Articles")
}
func (s *Store) PostDir(siteID, postID string) string {
	return filepath.Join(s.ArticlesDir(siteID), postID)
}

func (s *Store) Sites() ([]*Site, error) {
	entries, err := os.ReadDir(s.SitesDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Site
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		site, err := s.Site(e.Name())
		if err != nil {
			continue // a folder without planet.json is not a site
		}
		out = append(out, site)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created < out[j].Created })
	return out, nil
}

func (s *Store) Site(id string) (*Site, error) {
	var site Site
	if err := readJSON(filepath.Join(s.SiteDir(id), "planet.json"), &site); err != nil {
		return nil, err
	}
	return &site, nil
}

func (s *Store) SaveSite(site *Site) error {
	return writeJSON(filepath.Join(s.SiteDir(site.ID), "planet.json"), site)
}

func (s *Store) DeleteSite(id string) error {
	if err := os.RemoveAll(s.SiteDir(id)); err != nil {
		return err
	}
	return os.RemoveAll(s.PublicDir(id))
}

func (s *Store) Posts(siteID string) ([]*Post, error) {
	entries, err := os.ReadDir(s.ArticlesDir(siteID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Post
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var p Post
		if err := readJSON(filepath.Join(s.ArticlesDir(siteID), e.Name()), &p); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, &p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out, nil
}

func (s *Store) Post(siteID, postID string) (*Post, error) {
	var p Post
	if err := readJSON(filepath.Join(s.ArticlesDir(siteID), postID+".json"), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) SavePost(siteID string, p *Post) error {
	return writeJSON(filepath.Join(s.ArticlesDir(siteID), p.ID+".json"), p)
}

func (s *Store) DeletePost(siteID, postID string) error {
	if err := os.Remove(filepath.Join(s.ArticlesDir(siteID), postID+".json")); err != nil {
		return err
	}
	os.RemoveAll(s.PostDir(siteID, postID))
	return nil
}

func (s *Store) TemplateSettings(siteID string) (map[string]any, error) {
	m := map[string]any{}
	err := readJSON(filepath.Join(s.SiteDir(siteID), "templateSettings.json"), &m)
	if errors.Is(err, ErrNotFound) {
		return m, nil
	}
	return m, err
}

func (s *Store) SaveTemplateSettings(siteID string, m map[string]any) error {
	return writeJSON(filepath.Join(s.SiteDir(siteID), "templateSettings.json"), m)
}

// Ops records one-time derived-file operations, keyed the way Planet keys
// them, so imported sites keep their generated files untouched.
func (s *Store) Ops(siteID string) (map[string]AppleTime, error) {
	m := map[string]AppleTime{}
	err := readJSON(filepath.Join(s.SiteDir(siteID), "ops.json"), &m)
	if errors.Is(err, ErrNotFound) {
		return m, nil
	}
	return m, err
}

func (s *Store) RecordOp(siteID, key string) error {
	m, err := s.Ops(siteID)
	if err != nil {
		return err
	}
	m[key] = Now()
	return writeJSON(filepath.Join(s.SiteDir(siteID), "ops.json"), m)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s: %w", path, ErrNotFound)
		}
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

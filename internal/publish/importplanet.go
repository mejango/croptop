package publish

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/store"
)

// DefaultPlanetContainer is where the Croptop Mac app keeps its data.
func DefaultPlanetContainer() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Containers", "xyz.planetable.Lite", "Data")
}

// ImportPlanet copies sites, published files, and IPNS keys out of a
// Planet/Croptop container. It reads files only, so it works while the Mac
// app is running. Generated files (covers, nft.json) are kept as they are so
// CIDs already used onchain stay valid.
func ImportPlanet(st *store.Store, ks *ipfs.Keystore, container string, force bool, log func(string)) ([]string, error) {
	if log == nil {
		log = func(string) {}
	}
	myDir := filepath.Join(container, "Documents", "Planet", "My")
	pubDir := filepath.Join(container, "Documents", "Planet", "Public")
	keyDir := filepath.Join(container, "Library", "Application Support", "ipfs", "keystore")
	entries, err := os.ReadDir(myDir)
	if err != nil {
		return nil, fmt.Errorf("no Planet library at %s: %w", myDir, err)
	}
	var imported []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if _, err := os.Stat(filepath.Join(myDir, id, "planet.json")); err != nil {
			continue
		}
		if _, err := os.Stat(st.SiteDir(id)); err == nil && !force {
			return imported, fmt.Errorf("site %s already exists here; use --force to overwrite", id)
		}
		os.RemoveAll(st.SiteDir(id))
		if err := os.CopyFS(st.SiteDir(id), os.DirFS(filepath.Join(myDir, id))); err != nil {
			return imported, fmt.Errorf("copy %s: %w", id, err)
		}
		if _, err := os.Stat(filepath.Join(pubDir, id)); err == nil {
			os.RemoveAll(st.PublicDir(id))
			if err := os.CopyFS(st.PublicDir(id), os.DirFS(filepath.Join(pubDir, id))); err != nil {
				return imported, fmt.Errorf("copy public %s: %w", id, err)
			}
		}
		site, err := st.Site(id)
		if err != nil {
			return imported, err
		}
		site.IPNSSequence = 0 // the first publish learns the real one from the network
		site.PublishedElsewhere = false
		if err := st.SaveSite(site); err != nil {
			return imported, err
		}
		posts, err := st.Posts(id)
		if err != nil {
			return imported, err
		}
		for _, p := range posts {
			names := append([]string{}, p.Attachments...)
			names = append(names, "_cover.png", "_videoThumbnail.png")
			for _, name := range names {
				src := filepath.Join(st.PublicDir(id), p.ID, name)
				if _, err := os.Stat(src); err != nil {
					continue
				}
				if err := copyFile(src, filepath.Join(st.PostDir(id, p.ID), name)); err != nil {
					return imported, err
				}
			}
		}
		keyFile := filepath.Join(keyDir, ipfs.KeystoreFilename(id))
		if raw, err := os.ReadFile(keyFile); err == nil {
			if err := ks.ImportRaw(id, raw); err != nil {
				return imported, fmt.Errorf("key for %s: %w", site.Name, err)
			}
		} else {
			log(fmt.Sprintf("warning: no IPNS key found for %s (%s); import it later with `croptop key import`", site.Name, id))
		}
		log(fmt.Sprintf("imported %s (%d posts)", site.Name, len(posts)))
		imported = append(imported, id)
	}
	return imported, nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

package render

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/mejango/croptop/internal/store"
)

const MobileDescriptorKey = "croptopMobile"

// MobileDescriptor binds keyless rendering to the template whose assets and
// existing pages the published tree carries. It is covered by the site's CID.
type MobileDescriptor struct {
	Version        int    `json:"version"`
	TemplateDigest string `json:"templateDigest"`
}

// MobileTemplateDigest includes exactly the source consumed by the renderer.
// WalkDir has lexical order, and lengths disambiguate both names and contents.
func MobileTemplateDigest(fsys fs.FS) (string, error) {
	if fsys == nil {
		return "", errors.New("template is unavailable")
	}
	h := sha256.New()
	foundMeta := false
	err := fs.WalkDir(fsys, ".", func(name string, ent fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ent.IsDir() {
			if name != "." && name != "templates" && name != "assets" && !strings.HasPrefix(name, "templates/") && !strings.HasPrefix(name, "assets/") {
				return fs.SkipDir
			}
			return nil
		}
		if name != "template.json" && !strings.HasPrefix(name, "templates/") && !strings.HasPrefix(name, "assets/") {
			return nil
		}
		info, err := ent.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("template source is not a regular file: %s", name)
		}
		f, err := fsys.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Fprintf(h, "%d:%s:%d:", len(name), name, info.Size())
		if _, err = io.Copy(h, f); err != nil {
			return err
		}
		foundMeta = foundMeta || name == "template.json"
		return nil
	})
	if err != nil {
		return "", err
	}
	if !foundMeta {
		return "", errors.New("template.json is missing")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CheckMobileCompatibility is the single compatibility rule used by onboarding
// and service preparation. Never substitute a template based on its name alone.
func CheckMobileCompatibility(site *store.Site, digest string) error {
	var descriptor MobileDescriptor
	if site == nil || json.Unmarshal(site.Raw[MobileDescriptorKey], &descriptor) != nil || descriptor.Version != 1 {
		return errors.New("connect this site from an updated Croptop publisher and publish it once to enable phone posting")
	}
	if len(digest) != sha256.Size*2 || descriptor.TemplateDigest != digest {
		return errors.New("this site's template differs from the phone publisher; update and publish the supported Croptop template before connecting")
	}
	return nil
}

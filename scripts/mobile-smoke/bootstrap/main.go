// mobile-smoke-bootstrap prepares a brand-new, isolated test site. Network
// publication is impossible without --publish AND --host https://crop.top.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

const fixtureKind = "croptop-isolated-mobile-smoke-v1"

type options struct {
	dir, host string
	publish   bool
}

// State contains public identity and local paths, never private-key contents.
type state struct {
	Kind           string `json:"kind"`
	SiteID         string `json:"siteID"`
	IPNS           string `json:"ipns"`
	InitialCID     string `json:"initialCID"`
	Host           string `json:"host"`
	KeyFile        string `json:"keyFile"`
	ImageFile      string `json:"imageFile"`
	TemplateDigest string `json:"templateDigest"`
	CreatedAt      string `json:"createdAt"`
	PublishedAt    string `json:"publishedAt,omitempty"`
}

func main() {
	var o options
	flag.StringVar(&o.dir, "dir", "", "required isolated task directory; must be new or this harness's existing fixture")
	flag.BoolVar(&o.publish, "publish", false, "explicitly publish this new fixture to the fixed production host")
	flag.StringVar(&o.host, "host", "", "must explicitly be https://crop.top with --publish")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := run(ctx, o, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "mobile smoke:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o options, output io.Writer) (err error) {
	if o.publish && o.host != publish.DefaultHost {
		return errors.New("live publication requires --publish --host https://crop.top")
	}
	if o.host != "" && o.host != publish.DefaultHost {
		return errors.New("the smoke fixture only supports https://crop.top")
	}
	if o.dir == "" || !filepath.IsAbs(o.dir) || filepath.Clean(o.dir) == string(filepath.Separator) {
		return errors.New("--dir must identify an absolute, isolated task directory")
	}
	dir := filepath.Clean(o.dir)
	if info, statErr := os.Lstat(dir); statErr == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("fixture directory must not be a file or symlink")
	}
	manifest := filepath.Join(dir, "fixture.json")
	var s state
	b, readErr := os.ReadFile(manifest)
	if readErr == nil {
		if json.Unmarshal(b, &s) != nil || s.Kind != fixtureKind || s.Host != publish.DefaultHost || s.KeyFile != filepath.Join(dir, "site-key.pem") || s.ImageFile != filepath.Join(dir, "synthetic.png") || s.SiteID == "" || s.IPNS == "" || s.InitialCID == "" {
			return errors.New("directory does not contain a valid isolated smoke fixture")
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	} else {
		entries, listErr := os.ReadDir(dir)
		if listErr != nil && !os.IsNotExist(listErr) {
			return listErr
		}
		if len(entries) != 0 {
			return errors.New("refusing a nonempty directory without this harness's fixture.json; select a new task directory")
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return err
		}
		s = state{Kind: fixtureKind, SiteID: store.NewID(), Host: publish.DefaultHost, KeyFile: filepath.Join(dir, "site-key.pem"), ImageFile: filepath.Join(dir, "synthetic.png"), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return err
		}
		if err := os.WriteFile(s.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
			return err
		}
		if err := syntheticImage(s.ImageFile); err != nil {
			return err
		}
	}
	// Private disables P2P listeners, discovery, bootstrap and routing writes.
	// Only the explicitly guarded Publisher.Push below can publish the fixture.
	node := ipfs.NewEmbedded(filepath.Join(dir, "data"))
	node.Private = true
	if err := node.Start(ctx); err != nil {
		return err
	}
	defer func() {
		if stopErr := node.Stop(); err == nil {
			err = stopErr
		}
	}()
	key, err := os.ReadFile(s.KeyFile)
	if err != nil {
		return err
	}
	if info, err := os.Stat(s.KeyFile); err != nil || info.Mode().Perm() != 0600 {
		return errors.New("fixture private key must have mode 0600")
	}
	if err := node.Keystore().ImportPEM(s.SiteID, key); err != nil {
		return err
	}
	name, err := node.Keystore().Name(s.SiteID)
	if err != nil {
		return err
	}
	if s.IPNS != "" && s.IPNS != name {
		return errors.New("fixture key does not match its recorded identity")
	}
	s.IPNS = name
	st := &store.Store{Root: filepath.Join(dir, "data")}
	p := &publish.Publisher{Store: st, Node: node, Render: &render.Renderer{Store: st, Templates: templates.FS, CIDs: node}}
	if s.InitialCID == "" {
		now := store.Now()
		site := &store.Site{ID: s.SiteID, IPNS: s.IPNS, Name: "Croptop mobile production smoke", About: "Isolated synthetic publishing fixture. No user content or keys.", TemplateName: "Croptop", Created: now, Updated: now}
		if err := site.SetStorage(store.StorageHosted); err != nil {
			return err
		}
		publish.SetHost(site, publish.DefaultHost)
		if err := st.SaveSite(site); err != nil {
			return err
		}
		if err := st.SaveTemplateSettings(site.ID, map[string]any{"maintenanceMessage": ""}); err != nil {
			return err
		}
		if err := p.Render.Render(ctx, site.ID); err != nil {
			return err
		}
		if s.InitialCID, err = node.AddDir(ctx, st.PublicDir(site.ID)); err != nil {
			return err
		}
		if s.TemplateDigest, err = render.MobileTemplateDigest(templates.FS); err != nil {
			return err
		}
		if err := saveState(manifest, s); err != nil {
			return err
		}
	}
	if o.publish && s.PublishedAt == "" {
		// A previous uncertain bootstrap may already have committed. Never
		// overwrite an advanced head with the empty initial site on a rerun.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Host+"/v0/host/keys/"+s.IPNS, nil)
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("initial publication preflight: %w", err)
		}
		_ = resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusNotFound:
			site, err := st.Site(s.SiteID)
			if err != nil {
				return err
			}
			if _, err := node.SignRecord(s.SiteID, s.InitialCID, 1); err != nil {
				return err
			}
			if err := p.Push(ctx, site, s.InitialCID, 1); err != nil {
				return err
			}
		case http.StatusOK:
		default:
			return fmt.Errorf("initial publication preflight returned HTTP %d; no publication attempted", resp.StatusCode)
		}
		head, err := p.InspectSite(ctx, s.Host, s.IPNS)
		if err != nil {
			return fmt.Errorf("verify initial publication: %w", err)
		}
		if head.CID != s.InitialCID || head.Sequence != 1 {
			return errors.New("fixture already has a different published head; refusing to overwrite it")
		}
		s.PublishedAt = time.Now().UTC().Format(time.RFC3339)
		if err := saveState(manifest, s); err != nil {
			return err
		}
	}
	// Stop before reporting success: the browser smoke cannot depend on this
	// local node's blockstore, listener, or daemon remaining alive.
	if err := node.Stop(); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(s)
}

func saveState(path string, value state) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", append(b, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func syntheticImage(path string) error {
	img := image.NewNRGBA(image.Rect(0, 0, 320, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			c := color.NRGBA{uint8(x * 255 / 319), uint8(y * 255 / 239), 140, 255}
			if (x/40+y/40)%2 == 0 {
				c.B = 240
			}
			img.SetNRGBA(x, y, c)
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

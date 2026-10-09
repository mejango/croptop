package mobile

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

// This exercises the HTTP operation protocol against the real renderer, signed
// IPNS records, incremental IPFS tree builder, and host. No internet, deployed
// service, or running desktop is used after the initial publication.
func TestMobilePublicationWithRealHostAndRestartRecovery(t *testing.T) {
	ctx := context.Background()
	newNode := func() *ipfs.Embedded {
		t.Helper()
		node := ipfs.NewEmbedded(t.TempDir())
		node.Offline = true
		if err := node.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = node.Stop() })
		return node
	}
	h := &host.Host{Domain: "127.0.0.1", DataDir: t.TempDir(), Engine: newNode()}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	hostServer := httptest.NewServer(h)
	t.Cleanup(hostServer.Close)
	getPublic := func(path string) []byte {
		t.Helper()
		requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		r, err := http.NewRequestWithContext(requestCtx, "GET", hostServer.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := hostServer.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("public %s: status=%d, error=%v, body=%s", path, resp.StatusCode, err, body)
		}
		return body
	}

	// The device keeps the actual site key; only signatures cross the API.
	_, deviceKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(deviceKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	const siteID = "FF5F456D-904F-4EE6-8BB5-AD175C65319A"
	laptop := newNode()
	if err := laptop.Keystore().ImportPEM(siteID, keyPEM); err != nil {
		t.Fatal(err)
	}
	name, err := laptop.Keystore().Name(siteID)
	if err != nil {
		t.Fatal(err)
	}
	st := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(st.SiteDir(siteID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	site, err := st.Site(siteID)
	if err != nil {
		t.Fatal(err)
	}
	site.IPNS, site.Domain = name, nil
	if err := site.SetStorage(store.StorageHosted); err != nil {
		t.Fatal(err)
	}
	publish.SetHost(site, hostServer.URL)
	if err := st.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	desktop := &publish.Publisher{Store: st, Node: laptop, Render: &render.Renderer{Store: st, Templates: templates.FS, CIDs: laptop}}
	if err := desktop.Render.Render(ctx, siteID); err != nil {
		t.Fatal(err)
	}
	const unrelated = "previous publication's custom file must survive phone posting"
	if err := os.WriteFile(filepath.Join(st.PublicDir(siteID), "custom.txt"), []byte(unrelated), 0600); err != nil {
		t.Fatal(err)
	}
	oldPosts, err := st.Posts(siteID)
	if err != nil || len(oldPosts) == 0 {
		t.Fatalf("fixture posts: %v", err)
	}
	preserved := map[string][]byte{}
	for _, path := range []string{"custom.txt", "index.html", oldPosts[0].ID + "/article.json", oldPosts[0].ID + "/index.html"} {
		preserved[path], err = os.ReadFile(filepath.Join(st.PublicDir(siteID), filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
	}
	base, err := laptop.AddDir(ctx, st.PublicDir(siteID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := laptop.SignRecord(siteID, base, 5); err != nil {
		t.Fatal(err)
	}
	if err := desktop.Push(ctx, site, base, 5); err != nil {
		t.Fatal(err)
	}
	// Posting below must remain independent of the desktop and its blockstore.
	if err := laptop.Stop(); err != nil {
		t.Fatal(err)
	}

	newPublisher := func() (*publish.Publisher, *ipfs.Embedded) {
		node := newNode()
		source := &store.Store{Root: t.TempDir()}
		return &publish.Publisher{Store: source, Node: node, Render: &render.Renderer{Store: source, Templates: templates.FS, CIDs: node}}, node
	}
	backend, serviceNode := newPublisher()
	templateDigest, err := render.MobileTemplateDigest(templates.FS)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{Publisher: NewPrivatePublisher(backend), DataDir: t.TempDir(), Origin: "https://app.crop.top", HostURL: hostServer.URL, TemplateDigest: templateDigest, Enabled: true}
	f := &serviceFixture{server: server, handler: server.Handler(), key: deviceKey, name: name}
	if err := server.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.server.Close(); err != nil {
			t.Error(err)
		}
	})
	// Real template rendering under the race detector takes longer than the
	// fake publisher's ten-second fixture budget. Poll the public receipt while
	// allowing a bounded minute for the local renderer and host to finish.
	waitFor := func(id, state string) Operation {
		t.Helper()
		deadline := time.Now().Add(time.Minute)
		for {
			w := f.request(t, "GET", "/operations/"+id, nil, f.token)
			if w.Code != 200 && w.Code != 202 {
				t.Fatal(w.Body.String())
			}
			got := decodeOperation(t, w)
			f.server.mu.Lock()
			active := f.server.active[operationKey(f.name, id)] != nil
			f.server.mu.Unlock()
			if got.State == state && !active {
				return got
			}
			if got.State == "failed" || time.Now().After(deadline) {
				t.Fatalf("operation state=%s code=%s error=%s, want %s", got.State, got.Code, got.Error, state)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	prepare := func() Operation {
		t.Helper()
		f.enable(t)
		id := store.NewID()
		w := f.upload(t, id, "Screenshot")
		if w.Code != 202 && w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		return waitFor(id, "needs_signature")
	}
	f.token = f.login(t)
	w := f.request(t, "GET", "/site", nil, f.token)
	requireStatus(t, w, 200)
	var ready siteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &ready); err != nil || !ready.Ready || ready.CID != base || ready.Sequence != "5" {
		t.Fatalf("hosted readiness: %s, error=%v", w.Body, err)
	}
	op := prepare()
	if op.PostID != op.ID || op.Proposal.Parent != base || op.Proposal.Sequence != "6" {
		t.Fatalf("unexpected preparation: %+v", op)
	}
	// Preparing an image must not advance the published head before approval.
	before, err := backend.InspectSite(ctx, hostServer.URL, name)
	if err != nil || before.CID != base {
		t.Fatalf("unsigned preparation changed the public site: %+v, %v", before, err)
	}
	w = f.request(t, "GET", "/operations/"+op.ID+"/image", nil, f.token)
	requireStatus(t, w, 200)
	preview := bytes.Clone(w.Body.Bytes())
	if digest(preview) != op.MediaSHA256 {
		t.Fatal("signed preview does not identify the normalized image")
	}
	w = f.commit(t, op)
	if w.Code != 202 && w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	published := waitFor(op.ID, "published")
	w = f.request(t, "GET", "/operations/"+op.ID, nil, f.token)
	requireStatus(t, w, 200)
	if got := decodeOperation(t, w); got.State != "published" || got.URL == "" || !strings.HasSuffix(got.URL, "/"+op.ID+"/") {
		t.Fatalf("published receipt: %+v", got)
	}
	root := "/ipfs/" + op.Proposal.CID + "/"
	for path, original := range preserved {
		if got := getPublic(root + path); !bytes.Equal(got, original) {
			t.Fatalf("mobile publication changed unrelated %s", path)
		}
	}
	var article render.PublicPost
	if err := json.Unmarshal(getPublic(root+op.ID+"/article.json"), &article); err != nil {
		t.Fatal(err)
	}
	if article.ID != op.PostID || article.Title != op.Title || article.Content != op.Caption || article.Created.Unix() != op.CreatedAt || article.HeroImageFilename == nil || *article.HeroImageFilename != op.MediaSHA256+".png" {
		t.Fatalf("published screenshot fields: %+v", article)
	}
	if got := getPublic(root + op.ID + "/" + *article.HeroImageFilename); !bytes.Equal(got, preview) {
		t.Fatal("published hero differs from the image the device previewed")
	}
	if page := getPublic(root + op.ID + "/"); !bytes.Contains(page, []byte(op.Title)) {
		t.Fatal("published post has no rendered screenshot page")
	}

	// Advance the real host, then emulate a lost completion journal for the
	// earlier commit. Recovery must find its stable post identity in a later head.
	later := prepare()
	w = f.commit(t, later)
	if w.Code != 202 && w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	waitFor(later.ID, "published")
	f.server.mu.Lock()
	retained := f.server.operations[operationKey(name, op.ID)]
	retained.State, retained.URL = "committing", ""
	err = f.server.saveOperationLocked(retained)
	f.server.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	assertNoSiteKey := func(node *ipfs.Embedded) {
		t.Helper()
		entries, err := os.ReadDir(node.Keystore().Dir)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatal("phone publishing service retained a site private key")
		}
	}
	assertNoSiteKey(serviceNode)
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := serviceNode.Stop(); err != nil {
		t.Fatal(err)
	}
	restartedBackend, restartedNode := newPublisher()
	restarted := &Server{Publisher: NewPrivatePublisher(restartedBackend), DataDir: server.DataDir, Origin: server.Origin, HostURL: server.HostURL, TemplateDigest: server.TemplateDigest, Enabled: true}
	if err := restarted.Init(); err != nil {
		t.Fatalf("restart mobile service: %v", err)
	}
	f.server, f.handler = restarted, restarted.Handler()
	w = f.request(t, "GET", "/operations/"+op.ID, nil, f.token)
	requireStatus(t, w, 200)
	if recovered := decodeOperation(t, w); recovered.State != "published" || recovered.PostID != op.PostID || recovered.URL != published.URL {
		t.Fatalf("lost receipt after a later head was not recovered: %+v", recovered)
	}
	// Retries of the original signed request cannot publish another version.
	requireStatus(t, f.commit(t, op), 200)
	after, err := restartedBackend.InspectSite(ctx, hostServer.URL, name)
	if err != nil || after.CID != later.Proposal.CID || after.Sequence != 7 {
		t.Fatalf("retry advanced the host or lost its newer post: %+v, %v", after, err)
	}
	var posts []render.PublicPost
	if err := json.Unmarshal(after.Site.Raw["articles"], &posts); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, post := range posts {
		if post.ID == op.ID {
			count++
		}
	}
	if count != 1 || len(posts) != len(oldPosts)+2 {
		t.Fatalf("stable post identity lost: original count=%d, total posts=%d", count, len(posts))
	}
	assertNoSiteKey(restartedNode)
	if err := filepath.WalkDir(server.DataDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		contents, err := os.ReadFile(path)
		if err == nil && (bytes.Contains(contents, deviceKey.Seed()) || bytes.Contains(contents, keyPEM)) {
			t.Errorf("device private key was persisted in %s", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

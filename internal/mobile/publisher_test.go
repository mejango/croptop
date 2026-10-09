package mobile

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

func privateAdapterNode(t *testing.T) *ipfs.Embedded {
	t.Helper()
	node := ipfs.NewEmbedded(t.TempDir())
	node.Offline = true
	if err := node.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { node.Stop() })
	return node
}

func assertPrivateCacheEmpty(t *testing.T, root string) {
	t.Helper()
	files, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("private staging node retained after operation: %v", files)
	}
}

// Production deliberately gives the adapter the public host's publisher. Only
// its read-only inspections may touch that node: proposed draft blocks must
// remain absent until the owner authorizes the update and the host commits it.
func TestPrivatePublisherDoesNotExposeDraftBlocks(t *testing.T) {
	ctx := context.Background()
	publicNode := privateAdapterNode(t)
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: publicNode}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	owner := privateAdapterNode(t)
	st := &store.Store{Root: t.TempDir()}
	id := store.NewID()
	name, err := owner.Keystore().Generate(id)
	if err != nil {
		t.Fatal(err)
	}
	site := &store.Site{ID: id, IPNS: name, Name: "Private drafts", TemplateName: "Croptop", Created: store.Now(), Updated: store.Now()}
	if err := site.SetStorage(store.StorageHosted); err != nil {
		t.Fatal(err)
	}
	publish.SetHost(site, srv.URL)
	if err := st.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	original := &publish.Publisher{Store: st, Node: owner, Render: &render.Renderer{Store: st, Templates: templates.FS, CIDs: owner}}
	if err := original.Render.Render(ctx, id); err != nil {
		t.Fatal(err)
	}
	base, err := owner.AddDir(ctx, st.PublicDir(id))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.SignRecord(id, base, 1); err != nil {
		t.Fatal(err)
	}
	if err := original.Push(ctx, site, base, 1); err != nil {
		t.Fatal(err)
	}
	serviceStore := &store.Store{Root: t.TempDir()}
	publicPublisher := &publish.Publisher{Store: serviceStore, Node: publicNode, Render: &render.Renderer{Store: serviceStore, Templates: templates.FS, CIDs: publicNode}}
	private := NewPrivatePublisher(publicPublisher)
	private.tempRoot = t.TempDir()
	workDir := t.TempDir()
	image := filepath.Join(t.TempDir(), "screenshot.png")
	if err := os.WriteFile(image, []byte("this draft must remain private"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := private.PreparePost(ctx, srv.URL, name, publish.NewPost{ID: store.NewID(), Title: "A private screenshot", Files: []string{image}, HeroImage: "screenshot.png"}, workDir)
	if err != nil {
		t.Fatal(err)
	}
	assertPrivateCacheEmpty(t, private.tempRoot)
	if _, err := os.Stat(filepath.Join(prepared.ChangedDir, "planet.json")); err != nil {
		t.Fatalf("durable preparation removed: %v", err)
	}
	if _, err := publicNode.Block(ctx, prepared.CID); err == nil {
		t.Fatal("public host can read an unpublished draft's blocks")
	}
	if _, err := owner.Block(ctx, prepared.CID); err == nil {
		t.Fatal("draft escaped to the owner's node")
	}
	if found, err := private.InspectPost(ctx, srv.URL, name, prepared.PostID); err != nil || found != nil {
		t.Fatalf("draft is publicly visible: %+v, %v", found, err)
	}
	at := time.Now().Unix()
	u, _ := url.Parse(srv.URL)
	sig, err := owner.Keystore().Sign(id, host.PushMessage(u.Hostname(), name, prepared.CID, prepared.Sequence, at))
	if err != nil {
		t.Fatal(err)
	}
	record, err := owner.SignRecord(id, prepared.CID, prepared.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	posted, err := private.CommitPreparedPost(ctx, prepared, publish.PostAuthorization{Timestamp: at, Signature: sig, Record: record})
	if err != nil {
		t.Fatal(err)
	}
	assertPrivateCacheEmpty(t, private.tempRoot)
	if posted.CID != prepared.CID {
		t.Fatalf("committed another version: %+v", posted)
	}
	if _, err := publicNode.Block(ctx, prepared.CID); err != nil {
		t.Fatalf("authorized draft not available after commit: %v", err)
	}
	if found, err := private.InspectPost(ctx, srv.URL, name, prepared.PostID); err != nil || found == nil {
		t.Fatalf("published operation not recovered: %+v, %v", found, err)
	}
}

func TestPrivatePublisherCleansFailedAndCanceledOperations(t *testing.T) {
	base := &publish.Publisher{Render: &render.Renderer{Templates: templates.FS}}
	private := NewPrivatePublisher(base)
	private.tempRoot = t.TempDir()
	if _, err := private.PreparePost(context.Background(), "http://127.0.0.1", "invalid-ipns", publish.NewPost{Title: "Draft"}, t.TempDir()); err == nil {
		t.Fatal("invalid preparation succeeded")
	}
	assertPrivateCacheEmpty(t, private.tempRoot)
	if _, err := private.CommitPreparedPost(context.Background(), publish.PreparedPost{}, publish.PostAuthorization{}); err == nil {
		t.Fatal("invalid commit succeeded")
	}
	assertPrivateCacheEmpty(t, private.tempRoot)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := private.PreparePost(ctx, "http://127.0.0.1", "invalid-ipns", publish.NewPost{Title: "Draft"}, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preparation: %v", err)
	}
	assertPrivateCacheEmpty(t, private.tempRoot)
}

func TestPrivatePublisherRecoversCrashCacheWithoutRemovingDrafts(t *testing.T) {
	data := t.TempDir()
	cache := filepath.Join(data, "private-nodes")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	stale, err := os.MkdirTemp(cache, privateNodePrefix+"*")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "unpublished-block"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	drafts := filepath.Join(data, "operations")
	if err := os.Mkdir(drafts, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drafts, "draft.json"), []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A prefix alone does not establish ownership of a directory.
	unrelated := filepath.Join(cache, privateNodePrefix+"unrelated")
	if err := os.Mkdir(unrelated, 0o700); err != nil {
		t.Fatal(err)
	}
	private := NewPrivatePublisher(&publish.Publisher{Render: &render.Renderer{Templates: templates.FS}})
	if err := private.CleanupStaging(cache); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("crash cache remains: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated directory removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(drafts, "draft.json")); err != nil {
		t.Fatalf("durable draft removed: %v", err)
	}
	if private.tempRoot != cache {
		t.Fatalf("future caches not confined to service root: %s", private.tempRoot)
	}
	if err := private.CleanupStaging(data); err == nil {
		t.Fatal("accepted a broad cache root")
	}
}

func TestPrivatePublisherRejectsSymlinkCacheCleanup(t *testing.T) {
	data := t.TempDir()
	cache := filepath.Join(data, "private-nodes")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cache, privateNodePrefix+"12345")); err != nil {
		t.Fatal(err)
	}
	if err := NewPrivatePublisher(nil).CleanupStaging(cache); err == nil {
		t.Fatal("accepted a symlink cache")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("followed symlink during cleanup: %v", err)
	}
}

func TestPrivatePublisherStartupCleanupRequiresExclusiveServiceLock(t *testing.T) {
	data := t.TempDir()
	cache := filepath.Join(data, "private-nodes")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	stale, err := os.MkdirTemp(cache, privateNodePrefix+"*")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := lockDirectory(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close() })
	newServer := func() *Server {
		return &Server{Publisher: NewPrivatePublisher(&publish.Publisher{Render: &render.Renderer{Templates: templates.FS}}), DataDir: data,
			Origin: "https://app.crop.top", HostURL: "https://crop.top", TemplateDigest: "configured-template"}
	}
	if err := newServer().Init(); err == nil {
		t.Fatal("second service acquired an owned data directory")
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("second service deleted a running instance's cache: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := newServer()
	if err := restarted.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("restart did not remove abandoned cache: %v", err)
	}
}

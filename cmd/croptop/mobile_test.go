package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDedicatedMobileRuntimeIsPrivateAndHasNoGateway(t *testing.T) {
	t.Setenv("CROPTOP_MOBILE_PROXY_SECRET", "test-edge-secret")
	t.Setenv("CROPTOP_MOBILE_REQUIRE_HOSTED_SITE", "true")
	t.Setenv("CROPTOP_MOBILE_MAX_OPEN_OPERATIONS", "20")
	a := &app{dataDir: t.TempDir(), mobileOrigin: "https://composer.example", mobileHost: "https://crop.top"}
	runtime, err := a.openMobile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.close(); err != nil {
			t.Error(err)
		}
	})
	if !runtime.server.RequireHostedSite || runtime.server.MaxOpenOperations != 20 {
		t.Fatal("standalone service did not load operator enrollment/resource settings")
	}
	if !runtime.engine.Private || !runtime.engine.Offline || len(runtime.engine.RoutingPuts) != 0 || runtime.engine.PeersURL != "" {
		t.Fatal("dedicated publisher enabled public IPFS networking")
	}
	info, err := runtime.engine.Info(context.Background())
	if err != nil || info.Peers != 0 {
		t.Fatalf("private engine peers=%d err=%v", info.Peers, err)
	}
	if n := runtime.engine.ConnectLocalNodes(context.Background()); n != 0 {
		t.Fatalf("private engine discovered %d local peers", n)
	}
	for _, hostname := range []string{"composer.example", "service.railway.app", "crop.top", "alice.crop.top"} {
		for _, path := range []string{"/ipfs/bafy/index.html", "/ipns/k51test", "/routing/v1/ipns/k51test", "/v0/host/health", "/v0/planets/my", "/alice", "/protocol.test.mjs"} {
			w := httptest.NewRecorder()
			runtime.handler.ServeHTTP(w, httptest.NewRequest("GET", "https://"+hostname+path, nil))
			if w.Code != 404 {
				t.Fatalf("%s%s status %d, want 404", hostname, path, w.Code)
			}
		}
	}
	for _, test := range []struct {
		host, path string
		status     int
	}{
		{"composer.example", "/", 200},
		{"service.railway.app", "/", 404},
		{"service.railway.app", "/v0/mobile/health", 200},
		{"service.railway.app", "/v0/mobile/config", 403},
	} {
		w := httptest.NewRecorder()
		runtime.handler.ServeHTTP(w, httptest.NewRequest("GET", "https://"+test.host+test.path, nil))
		if w.Code != test.status {
			t.Fatalf("%s%s status %d, want %d: %s", test.host, test.path, w.Code, test.status, w.Body.String())
		}
		if test.path == "/" && test.status == 200 && !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatal("composer lacks its trusted-origin policy")
		}
	}
	if info, err := os.Stat(a.dataDir); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("private root permissions: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "host")); !os.IsNotExist(err) {
		t.Fatal("dedicated service initialized public host state")
	}
	if err := runtime.close(); err != nil {
		t.Fatal(err)
	}
	if runtime.engine.Running() {
		t.Fatal("private engine stayed running after shutdown")
	}
	// Closing releases the journal and engine datastore locks, permitting a
	// clean service restart on the same persistent Railway volume.
	restarted, err := a.openMobile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.close(); err != nil {
		t.Fatal(err)
	}
}

func TestDedicatedMobileRejectsMissingOrUnsafeOrigin(t *testing.T) {
	for _, origin := range []string{"", "http://composer.example", "https://crop.top", "https://unreserved.crop.top"} {
		a := &app{dataDir: t.TempDir(), mobileOrigin: origin, mobileHost: "https://crop.top"}
		if runtime, err := a.openMobile(context.Background()); err == nil {
			_ = runtime.close()
			t.Fatalf("unsafe origin %q accepted", origin)
		}
	}
}

func TestDedicatedMobileRejectsInvalidResourceConfiguration(t *testing.T) {
	for _, test := range []struct{ key, value string }{
		{"CROPTOP_MOBILE_REQUIRE_HOSTED_SITE", "invalid"},
		{"CROPTOP_MOBILE_MAX_OPEN_OPERATIONS", "-1"},
		{"CROPTOP_MOBILE_MAX_OPEN_OPERATIONS", "invalid"},
	} {
		t.Run(test.key+test.value, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			a := &app{dataDir: t.TempDir(), mobileOrigin: "https://composer.example", mobileHost: "https://crop.top"}
			if runtime, err := a.openMobile(context.Background()); err == nil {
				_ = runtime.close()
				t.Fatalf("invalid %s accepted", test.key)
			}
			if a.engine.Running() {
				t.Fatal("failed configuration left private engine running")
			}
		})
	}
}

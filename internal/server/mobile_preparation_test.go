package server

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/mobile"
	"github.com/mejango/croptop/internal/store"
)

const preparationTestID = "77C93B28-F9A3-4BC7-9E69-D47D470ED3CC"
const preparationSecondID = "BB5B4231-FAE4-496A-A1AA-C31CAD386F2A"

func preparationPath(site *store.Site) string {
	return "/v0/croptop/sites/" + site.ID + "/phone/preparations"
}

func preparationBody(id string, hosting, publish bool) string {
	return fmt.Sprintf(`{"id":%q,"enableHosting":%t,"allowPublish":%t}`, id, hosting, publish)
}

func decodePreparation(t *testing.T, w *httptest.ResponseRecorder) phonePreparationStatus {
	t.Helper()
	var status phonePreparationStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("preparation %d: %s: %v", w.Code, w.Body, err)
	}
	return status
}

func awaitPreparation(t *testing.T, h http.Handler, site *store.Site, id string, want ...string) phonePreparationStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response := phoneRequest(t, h, "GET", preparationPath(site)+"/"+id, "", nil)
		if response.Code != 200 {
			t.Fatalf("status %d %s", response.Code, response.Body)
		}
		status := decodePreparation(t, response)
		for _, state := range want {
			if status.State == state {
				return status
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("preparation did not reach %v", want)
	return phonePreparationStatus{}
}

func phoneHostedFixture(t *testing.T, s *Server, site *store.Site, override func(http.ResponseWriter, *http.Request) bool) *atomic.Int64 {
	t.Helper()
	var calls atomic.Int64
	var service *httptest.Server
	var relay *mobile.PairingRelay
	service = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if override != nil && override(w, r) {
			return
		}
		switch r.URL.Path {
		case "/v0/mobile/config":
			writeJSON(w, 200, map[string]any{"enabled": true, "version": 1, "origin": service.URL})
		case "/v0/mobile/challenge":
			id := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
			expires := time.Now().Add(time.Minute).Unix()
			writeJSON(w, 200, map[string]any{"id": id, "expiresAt": expires, "message": fmt.Sprintf("croptop-mobile-session\n%s\n%s\n%s\n%d", service.URL, site.IPNS, id, expires)})
		case "/v0/mobile/session":
			writeJSON(w, 200, map[string]string{"token": base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))})
		case "/v0/mobile/site":
			writeJSON(w, 200, map[string]any{"ipns": site.IPNS, "cid": "bafy-existing-published-content", "sequence": "9", "ready": true})
		case "/v0/mobile/connection":
			writeJSON(w, 200, map[string]bool{"enabled": true})
		default:
			relay.ServeHTTP(w, r, site.IPNS)
		}
	}))
	relay = mobile.NewPairingRelay(service.URL)
	s.MobileOrigin = service.URL
	t.Cleanup(service.Close)
	return &calls
}

func markPhoneHosted(t *testing.T, s *Server, site *store.Site) {
	t.Helper()
	if err := site.SetStorage(store.StorageHosted); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveSite(site); err != nil {
		t.Fatal(err)
	}
}

func TestPhonePreparationReusesPublishedSiteWithoutRenderingOrPublishingEdits(t *testing.T) {
	s, site, h := phoneTestServer(t)
	markPhoneHosted(t, s, site)
	before, err := os.ReadFile(filepath.Join(s.Store.SiteDir(site.ID), "planet.json"))
	if err != nil {
		t.Fatal(err)
	}
	phoneHostedFixture(t, s, site, nil)
	// A held render gate and intentionally unconfigured Publisher prove the
	// ready path does not render, wait for another publish, or publish edits.
	s.mu.Lock()
	defer s.mu.Unlock()
	w := phoneRequest(t, h, "POST", preparationPath(site), preparationBody(preparationTestID, false, false), nil)
	if w.Code != 202 {
		t.Fatalf("start %d %s", w.Code, w.Body)
	}
	status := awaitPreparation(t, h, site, preparationTestID, "ready", "failed")
	if status.State != "ready" || status.Connection == nil || status.SiteID != site.ID || status.Connection.IPNS != site.IPNS {
		t.Fatalf("unexpected result: %+v", status)
	}
	if status.Deadline-status.StartedAt != 300 {
		t.Fatalf("unbounded deadline: %+v", status)
	}
	after, _ := os.ReadFile(filepath.Join(s.Store.SiteDir(site.ID), "planet.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("connecting changed local saved state")
	}
	deleted := phoneRequest(t, h, "DELETE", preparationPath(site)+"/"+preparationTestID, "", nil)
	if deleted.Code != 200 || decodePreparation(t, deleted).State != "cancelled" {
		t.Fatal("ready connection was not cancelled")
	}
	if qr := phoneRequest(t, h, "GET", "/v0/croptop/sites/"+site.ID+"/phone/"+status.Connection.ID+"/qr", "", nil); qr.Code != 404 {
		t.Fatal("cancelled preparation retained private QR")
	}
}

func TestPhonePreparationRequiresPublicationConsentBeforeStorageChange(t *testing.T) {
	s, site, h := phoneTestServer(t)
	phoneHostedFixture(t, s, site, nil)
	w := phoneRequest(t, h, "POST", preparationPath(site), preparationBody(preparationTestID, true, false), nil)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	status := awaitPreparation(t, h, site, preparationTestID, "failed")
	if status.Code != "publication_required" {
		t.Fatalf("wrong failure: %+v", status)
	}
	saved, _ := s.Store.Site(site.ID)
	if saved.HostingEnabled() {
		t.Fatal("publication consent prompt changed storage")
	}
}

func TestPhonePreparationIdentityAndConsentAreIdempotent(t *testing.T) {
	s, site, h := phoneTestServer(t)
	markPhoneHosted(t, s, site)
	started, release := make(chan struct{}), make(chan struct{})
	var configCalls atomic.Int64
	phoneHostedFixture(t, s, site, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/config") {
			if configCalls.Add(1) == 1 {
				close(started)
			}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		return false
	})
	body := preparationBody(preparationTestID, false, false)
	if w := phoneRequest(t, h, "POST", preparationPath(site), body, nil); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	<-started
	for i := 0; i < 3; i++ {
		if w := phoneRequest(t, h, "POST", preparationPath(site), body, nil); w.Code != 202 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if w := phoneRequest(t, h, "POST", preparationPath(site), preparationBody(preparationTestID, true, true), nil); w.Code != 409 || !strings.Contains(w.Body.String(), "preparation_mismatch") {
		t.Fatal(w.Code, w.Body)
	}
	if w := phoneRequest(t, h, "POST", preparationPath(site), preparationBody(preparationSecondID, false, false), nil); w.Code != 409 || !strings.Contains(w.Body.String(), "preparation_in_progress") {
		t.Fatal(w.Code, w.Body)
	}
	other := *site
	other.ID = preparationSecondID
	if err := s.Store.SaveSite(&other); err != nil {
		t.Fatal(err)
	}
	if w := phoneRequest(t, h, "POST", preparationPath(&other), body, nil); w.Code != 409 || !strings.Contains(w.Body.String(), "preparation_mismatch") {
		t.Fatal(w.Code, w.Body)
	}
	close(release)
	awaitPreparation(t, h, site, preparationTestID, "ready")
	if configCalls.Load() != 1 {
		t.Fatal("same ID started duplicate work")
	}
}

func TestPhonePreparationCancelBeforeStartCannotResurrect(t *testing.T) {
	s, site, h := phoneTestServer(t)
	calls := phoneHostedFixture(t, s, site, nil)
	path := preparationPath(site) + "/" + preparationTestID
	for i := 0; i < 2; i++ {
		w := phoneRequest(t, h, "DELETE", path, "", nil)
		if w.Code != 200 || decodePreparation(t, w).State != "cancelled" {
			t.Fatal(w.Code, w.Body)
		}
	}
	w := phoneRequest(t, h, "POST", preparationPath(site), preparationBody(preparationTestID, true, true), nil)
	if w.Code != 202 || decodePreparation(t, w).State != "cancelled" || calls.Load() != 0 {
		t.Fatal("cancel-before-start resurrected work")
	}
}

func TestPhonePreparationCancellationInterruptsGateWaitAndKeepsStatusResponsive(t *testing.T) {
	s, site, h := phoneTestServer(t)
	calls := phoneHostedFixture(t, s, site, nil)
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := phoneRequest(t, h, "POST", preparationPath(site), preparationBody(preparationTestID, true, true), nil); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	if w := phoneRequest(t, h, "DELETE", preparationPath(site)+"/"+preparationTestID, "", nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	status := awaitPreparation(t, h, site, preparationTestID, "cancelled")
	if time.Since(start) > time.Second || status.Connection != nil {
		t.Fatal("cancel waited for global gate or created a connection")
	}
	saved, _ := s.Store.Site(site.ID)
	if saved.HostingEnabled() {
		t.Fatal("cancelled gate waiter later enabled hosting")
	}
}

func TestPhonePreparationServiceFailureNeverPublishesPendingEdits(t *testing.T) {
	for _, test := range []struct {
		code   string
		status int
	}{{"unavailable", 503}, {"site_not_ready", 409}} {
		t.Run(test.code, func(t *testing.T) {
			s, site, h := phoneTestServer(t)
			markPhoneHosted(t, s, site)
			phoneHostedFixture(t, s, site, func(w http.ResponseWriter, r *http.Request) bool {
				if strings.HasSuffix(r.URL.Path, "/session") {
					writeJSON(w, test.status, map[string]string{"code": test.code, "error": "host temporarily unavailable"})
					return true
				}
				return false
			})
			phoneRequest(t, h, "POST", preparationPath(site), preparationBody(preparationTestID, true, true), nil)
			status := awaitPreparation(t, h, site, preparationTestID, "failed")
			if status.Code == "publication_required" || status.Connection != nil {
				t.Fatalf("outage treated as publication consent: %+v", status)
			}
			// The fixture Publisher has no engine/renderer: an accidental fallback
			// would panic instead of returning this controlled failed operation.
		})
	}
}

func TestPhoneServiceErrorsRetainCodeAndStatus(t *testing.T) {
	s, site, _ := phoneTestServer(t)
	phoneHostedFixture(t, s, site, func(w http.ResponseWriter, r *http.Request) bool {
		writeJSON(w, 429, map[string]string{"code": "rate_limited", "error": "Please wait"})
		return true
	})
	err := s.phoneAPI(context.Background(), "GET", "/config", "", nil, nil)
	service, ok := err.(*phoneServiceError)
	if !ok || service.Code != "rate_limited" || service.Status != 429 || service.Message != "Please wait" {
		t.Fatalf("lost structured error: %v", err)
	}
}

func TestPhonePreparationTrustAndBoundedTombstones(t *testing.T) {
	s, site, h := phoneTestServer(t)
	for _, method := range []string{"GET", "DELETE"} {
		w := phoneRequest(t, h, method, preparationPath(site)+"/"+preparationTestID, "", map[string]string{"Origin": "https://example.org"})
		if w.Code != 403 {
			t.Fatalf("foreign %s accepted", method)
		}
	}
	pending := s.phoneConnections()
	pending.mu.Lock()
	for i := 0; i < phonePreparationLimit; i++ {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
		pending.preparations[phonePreparationKey(site.ID, id)] = &phonePreparation{ExpiresAt: time.Now().Add(time.Minute), Status: phonePreparationStatus{ID: id, SiteID: site.ID, State: "cancelled"}}
	}
	pending.mu.Unlock()
	w := phoneRequest(t, h, "DELETE", preparationPath(site)+"/"+preparationTestID, "", nil)
	if w.Code != 429 {
		t.Fatal("unbounded preparation tombstones")
	}
	pending.mu.Lock()
	for _, job := range pending.preparations {
		job.ExpiresAt = time.Now().Add(-time.Second)
	}
	pending.mu.Unlock()
	w = phoneRequest(t, h, "DELETE", preparationPath(site)+"/"+preparationTestID, "", nil)
	if w.Code != 200 {
		t.Fatal("expired tombstones not pruned")
	}
}

func TestPhonePreparationCancelOrHostingOptOutDuringConfirmationStopsTransfer(t *testing.T) {
	for _, action := range []string{"cancel", "hosting opt-out"} {
		t.Run(action, func(t *testing.T) {
			s, site, h := phoneTestServer(t)
			markPhoneHosted(t, s, site)
			started, release := make(chan struct{}), make(chan struct{})
			var blockStatus atomic.Bool
			var completions atomic.Int64
			phoneHostedFixture(t, s, site, func(w http.ResponseWriter, r *http.Request) bool {
				if strings.HasSuffix(r.URL.Path, "/complete") {
					completions.Add(1)
				}
				if r.Method == "GET" && strings.Contains(r.URL.Path, "/pairings/") && blockStatus.Load() {
					close(started)
					select {
					case <-release:
					case <-r.Context().Done():
					}
				}
				return false
			})
			phoneRequest(t, h, "POST", preparationPath(site), preparationBody(preparationTestID, false, false), nil)
			status := awaitPreparation(t, h, site, preparationTestID, "ready")
			link, err := url.Parse(status.Connection.URL)
			if err != nil {
				t.Fatal(err)
			}
			capability := strings.SplitN(strings.TrimPrefix(link.Fragment, "pair="), ".", 2)[1]
			receiver, err := ecdh.P256().GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]string{"receiverPublicKey": base64.StdEncoding.EncodeToString(receiver.PublicKey().Bytes())})
			req, _ := http.NewRequest("POST", s.MobileOrigin+"/v0/mobile/pairings/"+status.Connection.ID+"/claim", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Croptop-Pairing", capability)
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var info mobile.PairingInfo
			err = json.NewDecoder(response.Body).Decode(&info)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("claim failed: %d %v", response.StatusCode, err)
			}
			_, code, err := mobile.PairingSecrets(receiver, info.SenderPublicKey, info)
			if err != nil {
				t.Fatal(err)
			}
			blockStatus.Store(true)
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				result <- phoneRequest(t, h, "POST", "/v0/croptop/sites/"+site.ID+"/phone/"+status.Connection.ID+"/confirm", `{"code":"`+code+`"}`, nil)
			}()
			<-started
			if action == "cancel" {
				w := phoneRequest(t, h, "DELETE", preparationPath(site)+"/"+preparationTestID, "", nil)
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body)
				}
			} else {
				if err := site.SetStorage(store.StorageP2P); err != nil {
					t.Fatal(err)
				}
				if err := s.Store.SaveSite(site); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			select {
			case w := <-result:
				if w.Code == 200 || completions.Load() != 0 {
					t.Fatalf("late transfer after %s: status %d, completed %d", action, w.Code, completions.Load())
				}
			case <-time.After(time.Second):
				t.Fatal("confirmation did not stop")
			}
		})
	}
}

func TestPhoneBootstrapDoesNotEnrollAgainstOldHostedPolicy(t *testing.T) {
	s, site, h := phoneTestServer(t)
	var authRequests atomic.Int64
	phoneHostedFixture(t, s, site, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/session") || strings.HasSuffix(r.URL.Path, "/challenge") {
			authRequests.Add(1)
		}
		return false
	})
	phoneRequest(t, h, "POST", preparationPath(site), preparationBody(preparationTestID, true, true), nil)
	awaitPreparation(t, h, site, preparationTestID, "failed") // unconfigured test Publisher safely rejects bootstrap
	if authRequests.Load() != 0 {
		t.Fatal("enrollment started before bootstrap committed")
	}
}

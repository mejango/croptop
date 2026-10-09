package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/mobile"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

type preparationIntegrationTransport func(*http.Request) (*http.Response, error)

func (f preparationIntegrationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// Every key and blockstore in this test is disposable. The guard detects even
// ignored desktop announcement errors, and counts work the ready path must skip.
type preparationIntegrationNode struct {
	*ipfs.Embedded
	announcements atomic.Int64
	additions     atomic.Int64
	lookups       atomic.Int64
}

func (n *preparationIntegrationNode) NamePublish(context.Context, string, string, uint64) error {
	n.announcements.Add(1)
	return errors.New("desktop DHT announcement is forbidden during phone setup")
}

func (n *preparationIntegrationNode) AnnounceRecord(context.Context, string, string, []byte) error {
	n.announcements.Add(1)
	return errors.New("desktop DHT announcement is forbidden during phone setup")
}

func (n *preparationIntegrationNode) AddDir(ctx context.Context, dir string) (string, error) {
	n.additions.Add(1)
	return n.Embedded.AddDir(ctx, dir)
}

func (n *preparationIntegrationNode) NetworkRecord(ctx context.Context, name string) (*ipfs.Record, error) {
	n.lookups.Add(1)
	return n.Embedded.NetworkRecord(ctx, name)
}

// Exercise the actual consent -> publication -> hosted admission -> encrypted
// pairing pipeline, not a mock service that accepts an unhosted site. Both old
// local P2P sites and a previous attempt that already saved local hosted policy
// must repair the old public P2P descriptor before a session can be admitted.
func TestPhonePreparationRealHostedLegacyBootstrapAndReadyFastPath(t *testing.T) {
	for _, locallyHosted := range []bool{false, true} {
		name := "local_p2p"
		if locallyHosted {
			name = "local_hosted_public_p2p"
		}
		t.Run(name, func(t *testing.T) {
			ctx, stop := context.WithTimeout(context.Background(), 90*time.Second)
			defer stop()
			newNode := func() *ipfs.Embedded {
				t.Helper()
				n := ipfs.NewEmbedded(t.TempDir())
				n.Offline, n.Private = true, true
				if err := n.Start(ctx); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = n.Stop() })
				return n
			}
			publicNode := newNode()
			h := &host.Host{Domain: "crop.top", DataDir: t.TempDir(), Engine: publicNode}
			if err := h.Start(); err != nil {
				t.Fatal(err)
			}
			var armUpload, blocked, committed, hostVerificationBlocked, sessionBlocked, readinessBlocked atomic.Bool
			var sent, pushes, admitted atomic.Int64
			uploadStarted, uploadRelease := make(chan struct{}), make(chan struct{})
			hostVerificationStarted, hostVerificationRelease := make(chan struct{}), make(chan struct{})
			sessionStarted, sessionRelease := make(chan struct{}), make(chan struct{})
			readinessStarted, readinessRelease := make(chan struct{}), make(chan struct{})
			var releaseOnce, hostReleaseOnce, sessionReleaseOnce, readinessReleaseOnce sync.Once
			releaseUpload := func() { releaseOnce.Do(func() { close(uploadRelease) }) }
			releaseHostVerification := func() { hostReleaseOnce.Do(func() { close(hostVerificationRelease) }) }
			releaseSession := func() { sessionReleaseOnce.Do(func() { close(sessionRelease) }) }
			releaseReadiness := func() { readinessReleaseOnce.Do(func() { close(readinessRelease) }) }
			hostServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if committed.Load() && r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v0/host/keys/") && hostVerificationBlocked.CompareAndSwap(false, true) {
					close(hostVerificationStarted)
					select {
					case <-hostVerificationRelease:
					case <-r.Context().Done():
						return
					}
				}
				if r.Method == "POST" && r.URL.Path == "/v0/host/push" {
					pushes.Add(1)
					body, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, "upload read failed", 400)
						return
					}
					sent.Add(int64(len(body)))
					r.Body = io.NopCloser(bytes.NewReader(body))
					if armUpload.Load() && blocked.CompareAndSwap(false, true) {
						close(uploadStarted)
						select {
						case <-uploadRelease:
						case <-r.Context().Done():
							return
						}
					}
					answer := httptest.NewRecorder()
					h.ServeHTTP(answer, r)
					if armUpload.Load() && answer.Code == 200 && bytes.Contains(body, []byte(`name="manifest"`)) {
						committed.Store(true)
					}
					for k, values := range answer.Header() {
						w.Header()[k] = values
					}
					w.WriteHeader(answer.Code)
					_, _ = w.Write(answer.Body.Bytes())
					return
				}
				h.ServeHTTP(w, r)
			}))
			t.Cleanup(hostServer.Close)
			t.Cleanup(releaseUpload)
			t.Cleanup(releaseHostVerification)
			t.Cleanup(releaseSession)
			t.Cleanup(releaseReadiness)
			hostURL, _ := url.Parse(hostServer.URL)
			// The desktop deliberately accepts only its production host. Keep that
			// policy and real signatures intact while routing *all* HTTP locally.
			oldTransport := http.DefaultTransport
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			t.Cleanup(transport.CloseIdleConnections)
			var serviceAddress string
			http.DefaultTransport = preparationIntegrationTransport(func(r *http.Request) (*http.Response, error) {
				copy := r.Clone(r.Context())
				if r.URL.Scheme+"://"+r.URL.Host == publish.DefaultHost {
					copy.URL.Scheme, copy.URL.Host, copy.Host = "http", hostURL.Host, "crop.top"
				} else if r.URL.Scheme != "http" || (r.URL.Host != hostURL.Host && r.URL.Host != serviceAddress) {
					return nil, errors.New("non-fixture HTTP destination blocked")
				}
				return transport.RoundTrip(copy)
			})
			t.Cleanup(func() { http.DefaultTransport = oldTransport })

			laptop := &preparationIntegrationNode{Embedded: newNode()}
			st := &store.Store{Root: t.TempDir()}
			site := &store.Site{ID: store.NewID(), Name: "Synthetic phone setup", TemplateName: "Croptop", Created: store.Now(), Updated: store.Now()}
			var err error
			site.IPNS, err = laptop.Keystore().Generate(site.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := site.SetStorage(store.StorageP2P); err != nil {
				t.Fatal(err)
			}
			if err := st.SaveSite(site); err != nil {
				t.Fatal(err)
			}
			post := &store.Post{ID: store.NewID(), Title: "Existing synthetic post", Content: "Existing public content", Created: store.Now()}
			if err := st.SavePost(site.ID, post); err != nil {
				t.Fatal(err)
			}
			publisher := &publish.Publisher{Store: st, Node: laptop, Render: &render.Renderer{Store: st, Templates: templates.FS, CIDs: laptop}, SkipPrewarm: true}
			if err := publisher.Render.Render(ctx, site.ID); err != nil {
				t.Fatal(err)
			}
			// Model an already-hosted old app's public metadata: no mobile
			// descriptor and P2P policy, irrespective of the local checkbox.
			publicPath := filepath.Join(st.PublicDir(site.ID), "planet.json")
			publicJSON, err := os.ReadFile(publicPath)
			if err != nil {
				t.Fatal(err)
			}
			var legacy map[string]json.RawMessage
			if err := json.Unmarshal(publicJSON, &legacy); err != nil {
				t.Fatal(err)
			}
			delete(legacy, render.MobileDescriptorKey)
			publicJSON, err = json.Marshal(legacy)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(publicPath, publicJSON, 0600); err != nil {
				t.Fatal(err)
			}
			legacyCID, err := laptop.AddDir(ctx, st.PublicDir(site.ID))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := laptop.SignRecord(site.ID, legacyCID, 2); err != nil {
				t.Fatal(err)
			}
			if err := site.SetStorage(store.StorageHosted); err != nil {
				t.Fatal(err)
			}
			site.LastPublishedCID, site.IPNSSequence = &legacyCID, 2
			now := store.Now()
			site.LastPublished = &now
			if err := st.SaveSite(site); err != nil {
				t.Fatal(err)
			}
			if err := publisher.Push(ctx, site, legacyCID, 2); err != nil {
				t.Fatal(err)
			}
			if !locallyHosted {
				if err := site.SetStorage(store.StorageP2P); err != nil {
					t.Fatal(err)
				}
				if err := st.SaveSite(site); err != nil {
					t.Fatal(err)
				}
			}
			oldHead, err := publisher.InspectSite(ctx, publish.DefaultHost, site.IPNS)
			if err != nil || oldHead.Site == nil || oldHead.Site.HostingEnabled() || len(oldHead.Site.Raw[render.MobileDescriptorKey]) != 0 {
				t.Fatalf("fixture is not legacy public P2P: %v", err)
			}
			const attachment = "large-phone-bootstrap.bin"
			large := bytes.Repeat([]byte{0x5a}, 17<<20)
			if err := os.MkdirAll(st.PostDir(site.ID, post.ID), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(st.PostDir(site.ID, post.ID), attachment), large, 0600); err != nil {
				t.Fatal(err)
			}
			post.Attachments = []string{attachment}
			if err := st.SavePost(site.ID, post); err != nil {
				t.Fatal(err)
			}

			digest, err := render.MobileTemplateDigest(templates.FS)
			if err != nil {
				t.Fatal(err)
			}
			backendStore := &store.Store{Root: t.TempDir()}
			backend := &publish.Publisher{Store: backendStore, Node: publicNode, Render: &render.Renderer{Store: backendStore, Templates: templates.FS, CIDs: publicNode}}
			service := &mobile.Server{Publisher: mobile.NewPrivatePublisher(backend), DataDir: t.TempDir(), HostURL: publish.DefaultHost, TemplateDigest: digest, Enabled: true, RequireHostedSite: true}
			serviceHTTP := httptest.NewUnstartedServer(nil)
			service.Origin = "http://" + serviceHTTP.Listener.Addr().String()
			serviceAddress = serviceHTTP.Listener.Addr().String()
			serviceHandler := service.Handler()
			if err := service.Init(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			serviceHTTP.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v0/mobile/session" {
					if committed.Load() && sessionBlocked.CompareAndSwap(false, true) {
						close(sessionStarted)
						select {
						case <-sessionRelease:
						case <-r.Context().Done():
							return
						}
					}
					answer := httptest.NewRecorder()
					serviceHandler.ServeHTTP(answer, r)
					if answer.Code == 200 {
						admitted.Add(1)
						if !committed.Load() {
							t.Error("service admitted a session before the hosted manifest committed")
						}
					}
					for k, values := range answer.Header() {
						w.Header()[k] = values
					}
					w.WriteHeader(answer.Code)
					_, _ = w.Write(answer.Body.Bytes())
					return
				}
				if committed.Load() && r.URL.Path == "/v0/mobile/site" && readinessBlocked.CompareAndSwap(false, true) {
					close(readinessStarted)
					select {
					case <-readinessRelease:
					case <-r.Context().Done():
						return
					}
				}
				serviceHandler.ServeHTTP(w, r)
			})
			serviceHTTP.Start()
			t.Cleanup(serviceHTTP.Close)
			s := &Server{Store: st, Node: laptop, Pub: publisher, MobileOrigin: service.Origin}
			mux := http.NewServeMux()
			s.routesMobilePhone(mux)
			preparationID := store.NewID()
			t.Cleanup(func() { phoneRequest(t, mux, "DELETE", preparationPath(site)+"/"+preparationID, "", nil) })
			armUpload.Store(true)
			sent.Store(0)
			start := phoneRequest(t, mux, "POST", preparationPath(site), preparationBody(preparationID, true, true), nil)
			if start.Code != 202 {
				t.Fatalf("preparation start: %d", start.Code)
			}
			select {
			case <-uploadStarted:
			case <-ctx.Done():
				status := decodePreparation(t, phoneRequest(t, mux, "GET", preparationPath(site)+"/"+preparationID, "", nil))
				t.Fatalf("upload did not start: state=%s code=%s error=%s", status.State, status.Code, status.Error)
			}
			waiting := decodePreparation(t, phoneRequest(t, mux, "GET", preparationPath(site)+"/"+preparationID, "", nil))
			if waiting.State != "preparing" || waiting.Stage != "uploading" || waiting.Connection != nil || admitted.Load() != 0 {
				t.Fatalf("not waiting on hosting before admission: state=%s stage=%s sessions=%d", waiting.State, waiting.Stage, admitted.Load())
			}
			releaseUpload()
			assertPhase := func(started <-chan struct{}, want string) {
				t.Helper()
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatalf("%s phase did not start", want)
				}
				status := decodePreparation(t, phoneRequest(t, mux, "GET", preparationPath(site)+"/"+preparationID, "", nil))
				if status.State != "preparing" || status.Stage != want || status.Message != phoneStageMessage(want) || status.Connection != nil || !committed.Load() || strings.Contains(strings.ToLower(status.Message), "uploading") {
					t.Fatalf("post-upload phase was not truthful: state=%s stage=%s message=%s committed=%t", status.State, status.Stage, status.Message, committed.Load())
				}
			}
			assertPhase(hostVerificationStarted, "verifying_host")
			if admitted.Load() != 0 {
				t.Fatal("session admitted before signed host verification")
			}
			releaseHostVerification()
			assertPhase(sessionStarted, "verifying_phone")
			if admitted.Load() != 0 {
				t.Fatal("session was already admitted at enrollment barrier")
			}
			releaseSession()
			assertPhase(readinessStarted, "verifying_phone")
			if admitted.Load() == 0 {
				t.Fatal("readiness verification started before session admission")
			}
			releaseReadiness()
			await := func(id string) phonePreparationStatus {
				t.Helper()
				for {
					w := phoneRequest(t, mux, "GET", preparationPath(site)+"/"+id, "", nil)
					if w.Code != 200 {
						t.Fatalf("preparation poll: %d", w.Code)
					}
					status := decodePreparation(t, w)
					if status.State == "ready" {
						return status
					}
					if status.State == "failed" || ctx.Err() != nil {
						t.Fatalf("preparation: state=%s code=%s error=%s", status.State, status.Code, status.Error)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			ready := await(preparationID)
			if ready.Connection == nil || ready.Connection.IPNS != site.IPNS || admitted.Load() == 0 || sent.Load() < int64(len(large)) {
				t.Fatal("preparation returned without complete upload, admitted session, and pairing")
			}
			head, err := publisher.InspectSite(ctx, publish.DefaultHost, site.IPNS)
			if err != nil || head.CID == legacyCID || head.Site == nil || !head.Site.HostingEnabled() {
				t.Fatalf("legacy public policy was not repaired: %v", err)
			}
			if err := render.CheckMobileCompatibility(head.Site, digest); err != nil {
				t.Fatal(err)
			}
			attachmentRequest, err := http.NewRequestWithContext(ctx, "GET", publish.DefaultHost+"/ipfs/"+head.CID+"/"+post.ID+"/"+attachment, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.DefaultClient.Do(attachmentRequest)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.New()
			size, copyErr := io.Copy(hash, response.Body)
			response.Body.Close()
			expectedHash := sha256.Sum256(large)
			if response.StatusCode != 200 || copyErr != nil || size != int64(len(large)) || !bytes.Equal(hash.Sum(nil), expectedHash[:]) {
				t.Fatal("ready host does not serve the complete 17 MiB attachment")
			}
			assertPreparationEncryptedPairing(t, ctx, s, mux, site, ready.Connection)

			// Once the hosted version is compatible, pending desktop edits are
			// not consent to publish. A held render gate cannot stall connection.
			saved, err := st.Site(site.ID)
			if err != nil {
				t.Fatal(err)
			}
			saved.Name = "Unpublished local edit must remain private"
			if err := st.SaveSite(saved); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(st.SiteDir(site.ID), "planet.json"))
			if err != nil {
				t.Fatal(err)
			}
			pushCount, addCount, lookupCount := pushes.Load(), laptop.additions.Load(), laptop.lookups.Load()
			s.mu.Lock()
			defer s.mu.Unlock()
			fastID := store.NewID()
			t.Cleanup(func() { phoneRequest(t, mux, "DELETE", preparationPath(site)+"/"+fastID, "", nil) })
			if w := phoneRequest(t, mux, "POST", preparationPath(site), preparationBody(fastID, false, false), nil); w.Code != 202 {
				t.Fatalf("fast preparation start: %d", w.Code)
			}
			fast := awaitPreparation(t, mux, site, fastID, "ready", "failed")
			if fast.State != "ready" || fast.Connection == nil {
				t.Fatalf("ready site did not take the no-publication path: %s %s", fast.Code, fast.Error)
			}
			after, err := os.ReadFile(filepath.Join(st.SiteDir(site.ID), "planet.json"))
			if err != nil || !bytes.Equal(before, after) || pushes.Load() != pushCount || laptop.additions.Load() != addCount || laptop.lookups.Load() != lookupCount || laptop.announcements.Load() != 0 {
				t.Fatal("phone preparation published, rendered, rewrote saved edits, or used desktop DHT")
			}
			finalHead, err := publisher.InspectSite(ctx, publish.DefaultHost, site.IPNS)
			if err != nil || finalHead.CID != head.CID || finalHead.Site.Name == saved.Name {
				t.Fatal("connecting a second phone changed the published version")
			}
		})
	}
}

func assertPreparationEncryptedPairing(t *testing.T, ctx context.Context, s *Server, local http.Handler, site *store.Site, connection *phoneConnectionResult) {
	t.Helper()
	link, err := url.Parse(connection.URL)
	if err != nil {
		t.Fatal("invalid connection URL")
	}
	id, capability, ok := strings.Cut(strings.TrimPrefix(link.Fragment, "pair="), ".")
	if !ok || id != connection.ID {
		t.Fatal("invalid pairing fragment")
	}
	receiver, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	request := func(action string, body any, out any) {
		t.Helper()
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r, err := http.NewRequestWithContext(ctx, "POST", s.MobileOrigin+"/v0/mobile/pairings/"+id+"/"+action, bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Croptop-Pairing", capability)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal("pairing request failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(out) != nil {
			t.Fatalf("pairing %s failed: %d", action, resp.StatusCode)
		}
	}
	var claimed mobile.PairingInfo
	request("claim", map[string]string{"receiverPublicKey": base64.StdEncoding.EncodeToString(receiver.PublicKey().Bytes())}, &claimed)
	key, code, err := mobile.PairingSecrets(receiver, claimed.SenderPublicKey, claimed)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v0/croptop/sites/" + site.ID + "/phone/" + id
	qr := phoneRequest(t, local, "GET", path+"/qr", "", nil)
	if qr.Code != 200 || qr.Header().Get("Cache-Control") != "no-store" || !bytes.HasPrefix(qr.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("ready pairing has no private QR")
	}
	if w := phoneRequest(t, local, "POST", path+"/confirm", `{"code":"`+code+`"}`, nil); w.Code != 200 {
		t.Fatalf("desktop confirmation failed: %d", w.Code)
	}
	var envelope mobile.PairingInfo
	request("consume", map[string]bool{"confirmed": true}, &envelope)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := aead.Open(nil, nonce, encrypted, mobile.PairingTranscript(claimed))
	if err != nil {
		t.Fatal("phone could not decrypt the confirmed pairing")
	}
	var received struct {
		PEM, IPNS, Origin string
		Version           int
	}
	if err := json.Unmarshal(plaintext, &received); err != nil {
		t.Fatal(err)
	}
	expected, err := s.Node.Keystore().ExportPEM(site.ID)
	if err != nil || received.PEM != string(expected) || received.IPNS != site.IPNS || received.Origin != s.MobileOrigin || received.Version != 1 {
		t.Fatal("phone received the wrong synthetic identity")
	}
	status := phoneRequest(t, local, "GET", path, "", nil)
	if status.Code != 200 || !strings.Contains(status.Body.String(), "consumed") {
		t.Fatal("desktop did not observe consumed pairing")
	}
	if qr := phoneRequest(t, local, "GET", path+"/qr", "", nil); qr.Code != 404 {
		t.Fatal("consumed pairing retained its private QR")
	}
}

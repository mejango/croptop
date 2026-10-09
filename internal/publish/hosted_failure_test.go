package publish

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Each proxy wraps an actual local host, so fault injection distinguishes an
// accepted commit whose reply was lost from a fabricated success response.
func TestPublishHostedVerifiesCommitOutcome(t *testing.T) {
	for _, fault := range []string{"lost accepted response", "false success", "unverifiable committed head"} {
		t.Run(fault, func(t *testing.T) {
			r := newChangesRig(t)
			before, err := r.entry()
			if err != nil {
				t.Fatal(err)
			}
			var committed atomic.Int32
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if fault == "unverifiable committed head" && committed.Load() > 0 && strings.HasPrefix(req.URL.Path, "/routing/v1/ipns/") {
					w.Write([]byte("not a signed IPNS record"))
					return
				}
				if req.Method != "POST" || req.URL.Path != "/v0/host/push" {
					r.srv.Config.Handler.ServeHTTP(w, req)
					return
				}
				if fault == "false success" {
					w.Write([]byte(`{"cid":"not committed"}`))
					return
				}
				answer := httptest.NewRecorder()
				r.srv.Config.Handler.ServeHTTP(answer, req)
				if answer.Code != http.StatusOK {
					t.Errorf("injected lost response was not accepted: %d %s", answer.Code, answer.Body)
				}
				committed.Add(1)
				if fault == "lost accepted response" {
					http.Error(w, "injected response failure after accepting commit", http.StatusBadGateway)
					return
				}
				w.WriteHeader(answer.Code)
				w.Write(answer.Body.Bytes())
			}))
			t.Cleanup(proxy.Close)
			site, err := r.store.Site(fixtureID)
			if err != nil {
				t.Fatal(err)
			}
			SetHost(site, proxy.URL)
			if err := r.store.SaveSite(site); err != nil {
				t.Fatal(err)
			}
			r.edit("host-first commit verification")
			res, err := r.p.PublishHosted(context.Background(), fixtureID, nil)
			if fault == "lost accepted response" {
				if err != nil || res.CID == before.CID || committed.Load() != 1 {
					t.Fatalf("accepted commit was not recovered once: %+v, %v, commits=%d", res, err, committed.Load())
				}
				actual, verifyErr := r.p.InspectSite(context.Background(), r.srv.URL, r.ipns)
				if verifyErr != nil || actual.CID != res.CID || actual.Sequence != res.Sequence {
					t.Fatalf("recovered result is not the actual signed head: %+v, %v", actual, verifyErr)
				}
				return
			}
			if !errors.Is(err, ErrHostedOutcomeUnknown) || res.CID != "" {
				t.Fatalf("unverified commit returned success: %+v, %v", res, err)
			}
			saved, err := r.store.Site(fixtureID)
			if err != nil || deref(saved.LastPublishedCID) != before.CID {
				t.Fatalf("unverified result was recorded as published: %v", err)
			}
			if len(r.p.pending) != 0 || len(r.p.pushing) != 0 {
				t.Fatal("uncertain commit queued a background upload")
			}
		})
	}
}

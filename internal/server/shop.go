package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/shop"
	"github.com/mejango/croptop/internal/store"
)

func shopRequestAllowed(r *http.Request) bool {
	// Cross-origin forms, public site previews and opaque origins cannot start or
	// mutate sessions. Native clients have no Origin/Fetch Metadata headers.
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" || r.Header.Get("Sec-Fetch-Site") == "same-site" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
			return false
		}
	}
	if ref := r.Referer(); ref != "" {
		u, e := url.Parse(ref)
		if e != nil || u.Host != r.Host || (u.Path != "/" && u.Path != "/shop" && u.Path != "/shop-connect" && u.Path != "/shop-setup") {
			return false
		}
	}
	// A custom header also forces preflight for requests from unrelated websites.
	return r.Header.Get("X-Croptop-Shop") == "1"
}
func (s *Server) shopDir() string { return filepath.Join(s.Store.Root, "shop-sessions") }
func (s *Server) routesShop(mux *http.ServeMux) {
	s.routesShopConnect(mux)
	s.routesShopBundle(mux)
	mux.HandleFunc("GET /shop", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; connect-src 'self' https:; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		http.ServeFileFS(w, r, s.UI, "shop.html")
	})
	for _, name := range []string{"shop.js", "shop.css", "shop-logo.png"} {
		mux.HandleFunc("GET /"+name, func(w http.ResponseWriter, r *http.Request) { http.ServeFileFS(w, r, s.UI, name) })
	}
	// Only the built-in, versioned deployment code is trusted with the capability.
	// Site-authored templates and their scripts are never loaded in the wallet page.
	for _, name := range []string{"ethers.umd.min.js", "scripts/chains.js", "scripts/abis.js", "scripts/tx.js", "scripts/txs/croptop_deployer.js"} {
		mux.HandleFunc("GET /shop-assets/"+name, func(w http.ResponseWriter, r *http.Request) { http.ServeFileFS(w, r, s.Templates, "assets/"+name) })
	}
	mux.HandleFunc("POST /v0/croptop/sites/{id}/shop", s.openShop)
	mux.HandleFunc("GET /v0/croptop/sites/{id}/shop", s.shopStatus)
	mux.HandleFunc("POST /v0/croptop/sites/{id}/shop/{session}", s.updateShop)
}
func (s *Server) openShop(w http.ResponseWriter, r *http.Request) {
	if !shopRequestAllowed(r) {
		http.Error(w, "unrelated page", 403)
		return
	}
	if !shopSecureContext(r) {
		http.Error(w, "open shop creation on localhost or HTTPS", 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	site, ok := s.site(w, r)
	if !ok {
		return
	}
	session, err := shop.Load(s.shopDir(), site.ID)
	if errors.Is(err, os.ErrNotExist) {
		previous, e := s.Store.TemplateSettings(site.ID)
		if e != nil {
			writeErr(w, 500, e)
			return
		}
		session = &shop.Session{ID: shop.Random(), Token: shop.Random(), SiteID: site.ID, SiteName: site.Name, Salt: "0x" + shop.Random(), StartsAt: time.Now().Unix() + 360, Previous: previous, Chains: []shop.Chain{}}
		err = shop.Save(s.shopDir(), session)
	}
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"url": "/shop#" + site.ID + "/" + session.ID + "/" + session.Token})
}
func (s *Server) shopStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	site, ok := s.site(w, r)
	if !ok {
		return
	}
	session, err := shop.Load(s.shopDir(), site.ID)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, 200, map[string]any{"session": nil})
		return
	}
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	token := r.Header.Get("X-Shop-Token")
	if token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(session.Token)) != 1 {
		http.Error(w, "invalid session", 403)
		return
	}
	// The console can refresh addresses/progress, but only the wallet capability
	// can read transaction intents. Neither response ever returns the secret.
	session.Token = ""
	session.Previous = nil
	if token == "" {
		for i := range session.Chains {
			session.Chains[i].Data = ""
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"session": session})
}
func (s *Server) updateShop(w http.ResponseWriter, r *http.Request) {
	if !shopRequestAllowed(r) {
		http.Error(w, "unrelated page", 403)
		return
	}
	changed := false
	siteID := ""
	s.mu.Lock()
	defer func() {
		s.mu.Unlock()
		if changed && s.Pub != nil {
			s.render(r.Context(), siteID)
		}
	}()
	site, ok := s.site(w, r)
	if !ok {
		return
	}
	siteID = site.ID
	session, err := shop.Load(s.shopDir(), site.ID)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if session.ID != r.PathValue("session") || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Shop-Token")), []byte(session.Token)) != 1 {
		http.Error(w, "invalid session", 403)
		return
	}
	var in struct {
		Action                   string
		Config                   *shop.Config
		Chains                   []shop.Chain
		ChainID                  int
		From, Nonce, Hash, Value string
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err = json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, err)
		return
	}
	fail := func(message string) { writeErr(w, 409, errors.New(message)) }
	if in.Action == "edit" {
		for _, chain := range session.Chains {
			if len(chain.Attempts) > 0 {
				fail("recover the saved transactions before changing deployment details")
				return
			}
		}
		session.Config = nil
		session.Chains = []shop.Chain{}
		session.StartsAt = time.Now().Unix() + 360
	} else if in.Action == "plan" {
		if session.Config != nil {
			fail("deployment details are already saved")
			return
		}
		session.Config = in.Config
		session.Chains = in.Chains
		if err = session.ValidatePlan(); err != nil {
			writeErr(w, 400, err)
			return
		}
	} else {
		var ch *shop.Chain
		for i := range session.Chains {
			if session.Chains[i].ID == in.ChainID {
				ch = &session.Chains[i]
				break
			}
		}
		if ch == nil {
			fail("network is not in this session")
			return
		}
		var a *shop.Attempt
		if len(ch.Attempts) > 0 {
			a = &ch.Attempts[len(ch.Attempts)-1]
		}
		switch in.Action {
		case "reprice":
			if ch.Address != "" || a != nil && (a.State != "reverted" && a.State != "replaced") {
				fail("recover the existing transaction before changing its fee")
				return
			}
			if !shopQuantity(in.Value) || len(in.Value) > 66 {
				fail("invalid creation fee")
				return
			}
			ch.Value = in.Value
		case "signing":
			if ch.Address != "" || a != nil && (a.State == "pending" || a.State == "confirmed") {
				fail("recover the existing transaction before signing again")
				return
			}
			if !shop.Address(in.From) || len(in.Nonce) > 18 || !shopQuantity(in.Nonce) {
				fail("invalid wallet or nonce")
				return
			}
			if a != nil && (a.State == "signing" || a.State == "cancelled") {
				// A lost wallet response may already have broadcast. Every retry retains
				// its sender and nonce, including after a rejected replacement request.
				if !strings.EqualFold(a.From, in.From) || a.Nonce != in.Nonce {
					fail("resume with the original wallet and nonce")
					return
				}
				a.State = "signing"
			} else {
				ch.Attempts = append(ch.Attempts, shop.Attempt{From: strings.ToLower(in.From), Nonce: in.Nonce, State: "signing"})
			}
		case "cancelled":
			if a == nil || a.State != "signing" || a.Hash != "" {
				fail("transaction cannot be cancelled here")
				return
			}
			a.State = "cancelled"
		case "submitted":
			if a == nil || (a.State != "signing" && a.State != "pending") || !shop.Hash(in.Hash) {
				fail("invalid transaction")
				return
			}
			if a.Hash != "" && !strings.EqualFold(a.Hash, in.Hash) {
				fail("check the replacement transaction first")
				return
			}
			a.Hash = in.Hash
			a.State = "pending"
		case "verify":
			if a == nil || a.State == "reverted" || a.State == "replaced" {
				fail("no transaction to verify")
				return
			}
			hash := in.Hash
			if hash == "" {
				hash = a.Hash
			}
			ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
			defer cancel()
			rpc := s.shopRPC
			if rpc == nil {
				rpc = shop.Call
			}
			state, e := shop.Verify(ctx, rpc, ch, hash)
			if e != nil {
				writeErr(w, 502, e)
				return
			}
			// An unknown hash provides no evidence of a transaction. Keep the
			// original recoverable state until RPC observes its sender/nonce.
			if state != "unknown" {
				a.Hash = hash
				a.State = state
			}
		default:
			fail("unknown shop action")
			return
		}
	}
	// Journal confirmation before settings. Repeating verification repairs an
	// interrupted apply; Applied prevents overwriting later manual settings.
	if err = shop.Save(s.shopDir(), session); err != nil {
		writeErr(w, 500, err)
		return
	}
	for i := range session.Chains {
		ch := &session.Chains[i]
		if ch.Address == "" || ch.Applied {
			continue
		}
		settings, e := s.Store.TemplateSettings(site.ID)
		if e != nil {
			writeErr(w, 500, e)
			return
		}
		network, _ := shop.NetworkFor(ch.ID)
		key := network.Setting + "CollectionAddress"
		if !reflect.DeepEqual(settings[key], session.Previous[key]) && settings[key] != ch.Address {
			fail("shop settings changed while deploying; connect the confirmed address manually")
			return
		}
		settings[key] = ch.Address
		meta, e := s.metaFor(site)
		if e != nil {
			writeErr(w, 500, e)
			return
		}
		if _, exists := meta.Settings["collectionCategory"]; exists {
			settings["collectionCategory"] = "1"
		}
		site.Updated = store.Now()
		if e = s.Store.SaveSite(site); e == nil {
			e = s.Store.SaveTemplateSettings(site.ID, settings)
		}
		if e != nil {
			writeErr(w, 500, e)
			return
		}
		ch.Applied = true
		changed = true
		if e = shop.Save(s.shopDir(), session); e != nil {
			writeErr(w, 500, e)
			return
		}
	}
	session.Token = ""
	session.Previous = nil
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"session": session})
}
func shopQuantity(v string) bool {
	if len(v) < 3 || !strings.HasPrefix(v, "0x") {
		return false
	}
	for _, c := range v[2:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// PATCH merges only edited fields, so a settings window opened before wallet
// confirmation cannot replace newly confirmed addresses with stale defaults.
func (s *Server) saveShopAwareSettings(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	changed := false
	id := ""
	defer func() {
		s.mu.Unlock()
		if changed {
			s.render(r.Context(), id)
		}
	}()
	site, ok := s.site(w, r)
	if !ok {
		return
	}
	id = site.ID
	var values map[string]any
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
		writeErr(w, 400, err)
		return
	}
	if r.Method == "PATCH" {
		stored, err := s.Store.TemplateSettings(id)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		for k, v := range values {
			stored[k] = v
		}
		values = stored
	}
	site.Updated = store.Now()
	if err := s.Store.SaveSite(site); err != nil {
		writeErr(w, 500, err)
		return
	}
	if err := s.Store.SaveTemplateSettings(id, values); err != nil {
		writeErr(w, 500, err)
		return
	}
	changed = true
	writeJSON(w, 200, values)
}

func shopSecureContext(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.Host)
	if host == "" {
		host = r.Host
	}
	ip := net.ParseIP(host)
	return r.TLS != nil || host == "localhost" || (ip != nil && ip.IsLoopback())
}

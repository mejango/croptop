package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/shop"
	"github.com/mejango/croptop/internal/store"
)

func (s *Server) connectionDir(chain int) string {
	return filepath.Join(s.shopDir()+"-connect", strconv.Itoa(chain))
}
func (s *Server) connectionRPC() shop.RPC {
	if s.shopRPC != nil {
		return s.shopRPC
	}
	return shop.Call
}
func (s *Server) routesShopConnect(mux *http.ServeMux) {
	mux.HandleFunc("GET /shop-connect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; connect-src 'self' https:; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		http.ServeFileFS(w, r, s.UI, "shop-connect.html")
	})
	mux.HandleFunc("GET /shop-connect.js", func(w http.ResponseWriter, r *http.Request) { http.ServeFileFS(w, r, s.UI, "shop-connect.js") })
	mux.HandleFunc("POST /v0/croptop/sites/{id}/shop-connect", s.openShopConnection)
	mux.HandleFunc("GET /v0/croptop/sites/{id}/shop-connect/{chain}", s.shopConnection)
	mux.HandleFunc("POST /v0/croptop/sites/{id}/shop-connect/{chain}/{session}", s.shopConnection)
}
func (s *Server) openShopConnection(w http.ResponseWriter, r *http.Request) {
	if !shopRequestAllowed(r) {
		http.Error(w, "unrelated page", 403)
		return
	}
	if !shopSecureContext(r) {
		http.Error(w, "open shop setup on localhost or HTTPS", 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	site, ok := s.site(w, r)
	if !ok {
		return
	}
	var in struct{ Setting, Hook, Category string }
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
		writeErr(w, 400, e)
		return
	}
	var network shop.Network
	for _, n := range shop.Networks {
		if n.Setting+"CollectionAddress" == in.Setting {
			network = n
			break
		}
	}
	category, e := strconv.ParseUint(in.Category, 10, 24)
	if network.ID == 0 || !shop.Address(in.Hook) || e != nil {
		writeErr(w, 400, errors.New("enter a v6 shop address and category number"))
		return
	}
	dir := s.connectionDir(network.ID)
	session, e := shop.Load(dir, site.ID)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		writeErr(w, 500, e)
		return
	}
	if session != nil && session.Connection != nil {
		c := session.Connection
		if !c.Completed && c.Transaction != nil && c.Transaction.Attempt != nil && c.Transaction.Attempt.State != "confirmed" && c.Transaction.Attempt.State != "reverted" && c.Transaction.Attempt.State != "replaced" {
			if !strings.EqualFold(c.Hook, in.Hook) || c.Category != uint32(category) {
				writeErr(w, 409, errors.New("check the saved shop transaction on this network before connecting a different shop"))
				return
			}
		} else {
			session = nil
		}
	}
	if session == nil {
		previous, e := s.Store.TemplateSettings(site.ID)
		if e != nil {
			writeErr(w, 500, e)
			return
		}
		session = &shop.Session{ID: shop.Random(), Token: shop.Random(), SiteID: site.ID, SiteName: site.Name, Previous: previous, Chains: []shop.Chain{}, Connection: &shop.Connection{ChainID: network.ID, Hook: strings.ToLower(in.Hook), Category: uint32(category)}}
		if e := shop.Save(dir, session); e != nil {
			writeErr(w, 500, e)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]string{"url": "/shop-connect#" + site.ID + "/" + strconv.Itoa(network.ID) + "/" + session.ID + "/" + session.Token})
}
func (s *Server) shopConnection(w http.ResponseWriter, r *http.Request) {
	if !shopRequestAllowed(r) {
		http.Error(w, "unrelated page", 403)
		return
	}
	s.mu.Lock()
	changed := false
	siteID := ""
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
	chain, e := strconv.Atoi(r.PathValue("chain"))
	if e != nil {
		writeErr(w, 400, e)
		return
	}
	if _, ok := shop.NetworkFor(chain); !ok {
		writeErr(w, 400, errors.New("unsupported network"))
		return
	}
	dir := s.connectionDir(chain)
	session, e := shop.Load(dir, site.ID)
	if e != nil || session.Connection == nil {
		writeErr(w, 404, errors.New("open shop setup from site settings"))
		return
	}
	token := r.Header.Get("X-Shop-Token")
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(session.Token)) != 1 || (r.Method == "POST" && r.PathValue("session") != session.ID) {
		http.Error(w, "invalid session", 403)
		return
	}
	c := session.Connection
	if r.Method == "POST" {
		var in struct{ Action, From, Price, Nonce, Hash string }
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
			writeErr(w, 400, e)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		rpc := s.connectionRPC()
		fail := func(e error) { writeErr(w, 409, e) }
		switch in.Action {
		case "inspect", "plan":
			state, e := shop.ReadConnection(ctx, rpc, c, in.From, "latest")
			if e != nil {
				writeErr(w, 502, e)
				return
			}
			if in.Action == "plan" {
				if !shop.Address(in.From) {
					fail(errors.New("connect your wallet"))
					return
				}
				if e := shop.PlanConnection(c, state, in.From, in.Price); e != nil {
					fail(e)
					return
				}
			} else {
				c.State = state
			}
		case "signing":
			t := c.Transaction
			if t == nil || !shop.Address(in.From) || !shopQuantity(in.Nonce) || len(in.Nonce) > 18 {
				fail(errors.New("review the setup transaction first"))
				return
			}
			if t.Attempt != nil && t.Attempt.State != "signing" && t.Attempt.State != "cancelled" {
				fail(errors.New("check the saved transaction first"))
				return
			}
			state, e := shop.ReadConnection(ctx, rpc, c, in.From, "latest")
			if e != nil {
				writeErr(w, 502, e)
				return
			}
			next, e := shop.NextSetup(c, state, in.From)
			if e != nil {
				fail(e)
				return
			}
			if next == nil || next.To != t.To || next.Data != t.Data || c.PlanState == nil || state.Owner != c.PlanState.Owner || state.ProjectID != c.PlanState.ProjectID || (t.Kind == "criteria" && !reflect.DeepEqual(state.Criteria, c.PlanState.Criteria)) {
				fail(errors.New("shop permissions or posting rules changed; check the transaction or review again"))
				return
			}
			if t.Attempt != nil && (!strings.EqualFold(t.Attempt.From, in.From) || t.Attempt.Nonce != in.Nonce) {
				fail(errors.New("resume with the original wallet and nonce"))
				return
			}
			t.Attempt = &shop.Attempt{From: strings.ToLower(in.From), Nonce: in.Nonce, State: "signing"}
		case "cancelled":
			t := c.Transaction
			if t == nil || t.Attempt == nil || t.Attempt.State != "signing" || t.Attempt.Hash != "" {
				fail(errors.New("no signing request to cancel"))
				return
			}
			t.Attempt.State = "cancelled"
		case "submitted":
			t := c.Transaction
			if t == nil || t.Attempt == nil || !shop.Hash(in.Hash) || (t.Attempt.State != "signing" && t.Attempt.State != "pending") || (t.Attempt.Hash != "" && !strings.EqualFold(t.Attempt.Hash, in.Hash)) {
				fail(errors.New("invalid transaction hash"))
				return
			}
			t.Attempt.Hash = in.Hash
			t.Attempt.State = "pending"
		case "verify":
			t := c.Transaction
			if t == nil || t.Attempt == nil {
				fail(errors.New("no transaction to check"))
				return
			}
			hash := in.Hash
			if hash == "" {
				hash = t.Attempt.Hash
			}
			state, e := shop.VerifySetup(ctx, rpc, c, hash)
			if e != nil {
				writeErr(w, 502, e)
				return
			}
			if state != "unknown" {
				t.Attempt.Hash = hash
				t.Attempt.State = state
			}
			// A finalized reverted/replaced transaction can be reviewed afresh.
			if state == "reverted" || state == "replaced" {
				c.Transaction = nil
			}
		case "finish":
			if c.Expected == nil {
				fail(errors.New("review your posting rules first"))
				return
			}
			// Read both finalized and current state. Neither a browser success flag nor
			// a pending transaction is enough to connect the shop.
			for _, block := range []string{"finalized", "latest"} {
				state, e := shop.ReadConnection(ctx, rpc, c, "", block)
				if e != nil {
					writeErr(w, 502, e)
					return
				}
				if !state.PublisherGranted || !reflect.DeepEqual(state.Criteria, *c.Expected) {
					fail(errors.New("posting permissions and rules are not confirmed yet; check again after network finality"))
					return
				}
				c.State = state
			}
			settings, e := s.Store.TemplateSettings(site.ID)
			if e != nil {
				writeErr(w, 500, e)
				return
			}
			network, _ := shop.NetworkFor(chain)
			key := network.Setting + "CollectionAddress"
			category := strconv.FormatUint(uint64(c.Category), 10)
			for k, v := range map[string]any{key: c.Hook, "collectionCategory": category} {
				if !reflect.DeepEqual(settings[k], session.Previous[k]) && !reflect.DeepEqual(settings[k], v) {
					fail(errors.New("shop settings changed; reopen setup with the current settings"))
					return
				}
			}
			settings[key] = c.Hook
			settings["collectionCategory"] = category
			site.Updated = store.Now()
			if e := s.Store.SaveTemplateSettings(site.ID, settings); e != nil {
				writeErr(w, 500, e)
				return
			}
			if e := s.Store.SaveSite(site); e != nil {
				writeErr(w, 500, e)
				return
			}
			c.Completed = true
			changed = true
		default:
			fail(errors.New("unknown shop setup action"))
			return
		}
		if e := shop.Save(dir, session); e != nil {
			writeErr(w, 500, e)
			return
		}
	}
	session.Token = ""
	session.Previous = nil
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"session": session})
}

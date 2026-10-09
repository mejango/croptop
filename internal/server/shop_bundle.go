package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/shop"
	"github.com/mejango/croptop/internal/store"
)

func (s *Server) bundleDir() string { return s.shopDir() + "-setup" }
func (s *Server) routesShopBundle(mux *http.ServeMux) {
	mux.HandleFunc("GET /shop-setup", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; connect-src 'self' https:; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		http.ServeFileFS(w, r, s.UI, "shop-setup.html")
	})
	mux.HandleFunc("GET /shop-setup.js", func(w http.ResponseWriter, r *http.Request) { http.ServeFileFS(w, r, s.UI, "shop-setup.js") })
	mux.HandleFunc("POST /v0/croptop/sites/{id}/shop-setup", s.openShopBundle)
	mux.HandleFunc("GET /v0/croptop/sites/{id}/shop-setup", s.shopBundle)
	mux.HandleFunc("POST /v0/croptop/sites/{id}/shop-setup/{session}", s.shopBundle)
}
func (s *Server) openShopBundle(w http.ResponseWriter, r *http.Request) {
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
	var in struct {
		Targets  map[string]string `json:"targets"`
		Category string            `json:"category"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, err)
		return
	}
	category, err := strconv.ParseUint(in.Category, 10, 24)
	if err != nil || len(in.Targets) > 4 {
		writeErr(w, 400, errors.New("enter the shop addresses for one network family and a category number"))
		return
	}
	chains := []*shop.BundleChain{}
	var testnet bool
	for _, n := range shop.Networks {
		hook, ok := in.Targets[n.Setting+"CollectionAddress"]
		if !ok {
			continue
		}
		if !shop.Address(hook) || len(chains) > 0 && n.Testnet != testnet {
			writeErr(w, 400, errors.New("enter valid shop addresses on either production networks or testnets"))
			return
		}
		testnet = n.Testnet
		chains = append(chains, &shop.BundleChain{Connection: &shop.Connection{ChainID: n.ID, Hook: strings.ToLower(hook), Category: uint32(category)}, Requests: []shop.ForwardRequest{}, Status: "Ready to review"})
		// Keep old per-network transaction recovery usable after upgrading.
		old, e := shop.Load(s.connectionDir(n.ID), site.ID)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			writeErr(w, 500, e)
			return
		}
		if old != nil && old.Connection != nil && !old.Connection.Completed && old.Connection.Transaction != nil && old.Connection.Transaction.Attempt != nil {
			a := old.Connection.Transaction.Attempt
			if a.State != "confirmed" && a.State != "reverted" && a.State != "replaced" {
				writeJSON(w, 200, map[string]string{"url": "/shop-connect#" + site.ID + "/" + strconv.Itoa(n.ID) + "/" + old.ID + "/" + old.Token})
				return
			}
		}
	}
	if len(chains) != len(in.Targets) {
		writeErr(w, 400, errors.New("unsupported shop network"))
		return
	}
	session, err := shop.Load(s.bundleDir(), site.ID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		writeErr(w, 500, err)
		return
	}
	if session != nil && session.Bundle != nil && session.Bundle.Active() {
		// Always resume live authorizations. A changed form must not hide a signed bundle.
	} else {
		previous, e := s.Store.TemplateSettings(site.ID)
		if e != nil {
			writeErr(w, 500, e)
			return
		}
		session = &shop.Session{ID: shop.Random(), Token: shop.Random(), SiteID: site.ID, SiteName: site.Name, Previous: previous, Chains: []shop.Chain{}, Bundle: &shop.SetupBundle{Chains: chains, Category: uint32(category), SourceChain: 1}}
		if e = shop.Save(s.bundleDir(), session); e != nil {
			writeErr(w, 500, e)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]string{"url": "/shop-setup#" + site.ID + "/" + session.ID + "/" + session.Token})
}
func (s *Server) shopBundle(w http.ResponseWriter, r *http.Request) {
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
	session, err := shop.Load(s.bundleDir(), site.ID)
	if err != nil || session.Bundle == nil {
		writeErr(w, 404, errors.New("open posting setup from site settings"))
		return
	}
	token := r.Header.Get("X-Shop-Token")
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(session.Token)) != 1 || r.Method == "POST" && r.PathValue("session") != session.ID {
		http.Error(w, "invalid session", 403)
		return
	}
	b := session.Bundle
	if r.Method == "POST" {
		var in struct {
			Action, From, Price, Signature, Nonce, Hash, ProjectID string
			Chain, Index                                           int
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16384)
		if err = json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, 400, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
		defer cancel()
		rpc := s.connectionRPC()
		action := func() error {
			if b.Completed {
				if in.Action == "check" {
					return nil
				}
				return errors.New("this setup is already complete")
			}
			switch in.Action {
			case "resolve":
				if b.Active() {
					return errors.New("finish the saved setup before choosing another project")
				}
				chains, err := shop.ResolveProject(ctx, rpc, in.Chain, in.ProjectID, b.Category)
				if err != nil {
					return err
				}
				for _, chain := range chains {
					old, e := shop.Load(s.connectionDir(chain.Connection.ChainID), site.ID)
					if e != nil && !errors.Is(e, os.ErrNotExist) {
						return e
					}
					if old != nil && old.Connection != nil && !old.Connection.Completed && old.Connection.Transaction != nil && old.Connection.Transaction.Attempt != nil {
						a := old.Connection.Transaction.Attempt
						if a.State != "confirmed" && a.State != "reverted" && a.State != "replaced" {
							return errors.New("check the saved posting transaction for this network before starting another setup")
						}
					}
				}
				b.Chains = chains
				b.Resolved = true
				b.SourceChain = in.Chain
				b.SourceProject = chains[0].Connection.State.ProjectID
			case "plan":
				if len(b.Chains) == 0 {
					return errors.New("find your project first")
				}
				return shop.PlanBundle(ctx, rpc, b, in.From, in.Price)
			case "signature":
				if b.Attempt != nil || b.Quote != nil {
					return errors.New("check the saved Relayr bundle")
				}
				if !regexp.MustCompile(`^0x[0-9a-fA-F]{130}$`).MatchString(in.Signature) {
					return errors.New("invalid wallet signature")
				}
				if !b.Active() || time.Now().Unix()+60 >= b.Deadline {
					return errors.New("review setup after the saved authorizations expire")
				}
				for _, chain := range b.Chains {
					if chain.Connection.ChainID == in.Chain && in.Index >= 0 && in.Index < len(chain.Requests) {
						request := &chain.Requests[in.Index]
						if request.Signature != "" && request.Signature != in.Signature {
							return errors.New("this request is already signed")
						}
						request.Signature = in.Signature
						chain.Status = "Wallet authorization saved"
						return nil
					}
				}
				return errors.New("unknown setup request")
			case "quote":
				if b.Attempt != nil {
					return errors.New("check the saved funding transaction before requesting another quote")
				}
				entries, err := shop.BundleEntries(ctx, rpc, b)
				if err != nil {
					return err
				}
				quote, err := shop.QuoteBundle(ctx, entries)
				if err != nil {
					return err
				}
				b.Quote = quote
			case "paying":
				if !strings.EqualFold(in.From, b.From) || !shopQuantity(in.Nonce) || len(in.Nonce) > 18 {
					return errors.New("use the wallet that signed this setup")
				}
				if b.Attempt != nil && (b.Attempt.State != "signing" && b.Attempt.State != "cancelled" || b.Attempt.From != strings.ToLower(in.From) || b.Attempt.Nonce != in.Nonce || b.Payment.Chain != in.Chain) {
					return errors.New("check the saved funding transaction; do not pay a second time")
				}
				if err := shop.ValidateBundleState(ctx, rpc, b); err != nil {
					return err
				}
				if b.Quote == nil {
					return errors.New("request a Relayr quote")
				}
				var payment *shop.RelayrPayment
				for _, p := range b.Quote.Payments {
					if p.Chain == in.Chain {
						copy := p
						payment = &copy
						break
					}
				}
				if payment == nil {
					return errors.New("choose a quoted funding network")
				}
				if err := shop.ValidatePayment(ctx, rpc, b, *payment); err != nil {
					return err
				}
				b.Payment = payment
				b.Attempt = &shop.Attempt{From: b.From, Nonce: in.Nonce, State: "signing"}
			case "cancelled":
				if b.Attempt == nil || b.Attempt.State != "signing" || b.Attempt.Hash != "" {
					return errors.New("no wallet request to cancel")
				}
				// Explicit wallet rejection is the only evidence that nothing broadcast.
				b.Attempt = nil
				b.Payment = nil
			case "submitted":
				if b.Attempt == nil || !shop.Hash(in.Hash) || b.Attempt.State != "signing" && b.Attempt.State != "pending" || b.Attempt.Hash != "" && !strings.EqualFold(b.Attempt.Hash, in.Hash) {
					return errors.New("invalid funding transaction")
				}
				b.Attempt.Hash = in.Hash
				b.Attempt.State = "pending"
			case "check":
				if b.Attempt != nil {
					hash := in.Hash
					if hash == "" {
						hash = b.Attempt.Hash
					}
					if hash != "" {
						state, e := shop.VerifyBundlePayment(ctx, rpc, b, hash)
						if e != nil {
							return e
						}
						if state != "unknown" {
							b.Attempt.Hash = hash
							b.Attempt.State = state
						}
					}
				}
				if shop.CheckBundle(ctx, rpc, b) {
					settings, e := s.Store.TemplateSettings(site.ID)
					if e != nil {
						return e
					}
					updates := map[string]any{}
					for _, chain := range b.Chains {
						c := chain.Connection
						n, _ := shop.NetworkFor(c.ChainID)
						updates[n.Setting+"CollectionAddress"] = c.Hook
						updates["collectionCategory"] = strconv.FormatUint(uint64(c.Category), 10)
					}
					for key, value := range updates {
						if !reflect.DeepEqual(settings[key], session.Previous[key]) && !reflect.DeepEqual(settings[key], value) {
							return errors.New("shop settings changed while setup was open; restore the reviewed addresses in settings before connecting")
						}
					}
					for key, value := range updates {
						settings[key] = value
					}
					if e = s.Store.SaveTemplateSettings(site.ID, settings); e != nil {
						return e
					}
					site.Updated = store.Now()
					if e = s.Store.SaveSite(site); e != nil {
						return e
					}
					b.Completed = true
					changed = true
				}
			default:
				return errors.New("unknown posting setup action")
			}
			return nil
		}
		if err = action(); err != nil {
			writeErr(w, 409, err)
			return
		}
		if err = shop.Save(s.bundleDir(), session); err != nil {
			writeErr(w, 500, err)
			return
		}
	}
	session.Token = ""
	session.Previous = nil
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"session": session})
}

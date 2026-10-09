package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/mobile"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/web"
)

// mobileHandler is shared by host and dedicated phone-service commands so
// template compatibility, private storage and trusted-origin routing agree.
func (a *app) mobileHandler(publicDomain string, fallback http.Handler) (*mobile.Server, http.Handler, error) {
	if err := mobile.ValidateComposerOrigin(a.mobileOrigin, publicDomain); err != nil {
		return nil, nil, err
	}
	digest, err := render.MobileTemplateDigest(a.tmpl)
	if err != nil {
		return nil, nil, fmt.Errorf("phone template: %w", err)
	}
	phone := &mobile.Server{Publisher: mobile.NewPrivatePublisher(a.pub), DataDir: filepath.Join(a.dataDir, "mobile"), Origin: a.mobileOrigin, HostURL: a.mobileHost, TemplateDigest: digest, Enabled: true}
	if strings.TrimSpace(a.mobileAllowSites) != "" {
		for _, name := range strings.Split(a.mobileAllowSites, ",") {
			phone.AllowSites = append(phone.AllowSites, strings.TrimSpace(name))
		}
	}
	phone.ProxySecret = os.Getenv("CROPTOP_MOBILE_PROXY_SECRET")
	if raw := os.Getenv("CROPTOP_MOBILE_REQUIRE_HOSTED_SITE"); raw != "" {
		phone.RequireHostedSite, err = strconv.ParseBool(raw)
		if err != nil {
			return nil, nil, errors.New("CROPTOP_MOBILE_REQUIRE_HOSTED_SITE must be true or false")
		}
	}
	if raw := os.Getenv("CROPTOP_MOBILE_MAX_OPEN_OPERATIONS"); raw != "" {
		phone.MaxOpenOperations, err = strconv.Atoi(raw)
		if err != nil || phone.MaxOpenOperations < 0 {
			return nil, nil, errors.New("CROPTOP_MOBILE_MAX_OPEN_OPERATIONS must be a nonnegative integer")
		}
	}
	if err := phone.Init(); err != nil {
		return nil, nil, fmt.Errorf("phone service: %w", err)
	}
	assets, err := fs.Sub(web.FS, "mobile")
	if err != nil {
		_ = phone.Close()
		return nil, nil, err
	}
	return phone, mobile.Gateway(phone, assets, a.tmpl, publicDomain, fallback), nil
}

type mobileRuntime struct {
	server  *mobile.Server
	handler http.Handler
	engine  *ipfs.Embedded
}

func (m *mobileRuntime) close() error {
	// Stop jobs before closing the base engine used for publication inspection.
	return errors.Join(m.server.Close(), m.engine.Stop())
}

// openMobile never starts host routes, DHT bootstrapping, local discovery,
// swarm listeners, followers or keepalive loops. The engine only verifies
// already-published blocks fetched from the fixed HTTPS publication host.
func (a *app) openMobile(ctx context.Context) (*mobileRuntime, error) {
	if a.mobileOrigin == "" {
		return nil, errors.New("--mobile-origin is required for the dedicated phone service")
	}
	u, err := url.Parse(a.mobileHost)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("--mobile-host must be a fixed publishing origin")
	}
	if err := mobile.ValidateComposerOrigin(a.mobileOrigin, u.Hostname()); err != nil {
		return nil, err
	}
	if err := a.open("embedded"); err != nil {
		return nil, err
	}
	if err := os.Chmod(a.dataDir, 0700); err != nil {
		return nil, err
	}
	e := a.engine.(*ipfs.Embedded)
	e.Private, e.RoutingPuts, e.PeersURL = true, nil, ""
	if err := e.Start(ctx); err != nil {
		_ = e.Stop()
		return nil, fmt.Errorf("private phone inspection engine: %w", err)
	}
	phone, handler, err := a.mobileHandler(u.Hostname(), http.NotFoundHandler())
	if err != nil {
		_ = e.Stop()
		return nil, err
	}
	return &mobileRuntime{server: phone, handler: handler, engine: e}, nil
}

func (a *app) mobile(listen string) error {
	if listen == "" {
		listen = "127.0.0.1:8090"
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	runtime, err := a.openMobile(ctx)
	if err != nil {
		return err
	}
	defer runtime.close()
	srv := &http.Server{Addr: listen, Handler: runtime.handler, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		// Cancel active jobs first; they retain recoverable operation journals.
		_ = runtime.server.Close()
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := srv.Shutdown(shutdown); err != nil {
			_ = srv.Close()
		}
	}()
	println(fmt.Sprintf("phone service for %s at http://%s (private IPFS; no gateway)", a.mobileOrigin, listen))
	err = srv.ListenAndServe()
	cancel()
	<-stopped
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

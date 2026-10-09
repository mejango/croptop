package main

import (
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"

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

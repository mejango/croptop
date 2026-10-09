package mobile

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
)

// ValidateComposerOrigin prevents a retired key origin from later becoming an
// author-controlled gateway hostname. app.<domain> is permanently reserved;
// other composer origins must be outside the public wildcard namespace.
func ValidateComposerOrigin(raw, publicDomain string) error {
	u, err := url.Parse(raw)
	if err != nil || !validOrigin(raw) || u.Path != "" {
		return fmt.Errorf("invalid phone composer origin")
	}
	host, domain := strings.ToLower(u.Hostname()), strings.ToLower(publicDomain)
	if host == domain || (strings.HasSuffix(host, "."+domain) && host != "app."+domain) {
		return fmt.Errorf("phone composer must use the reserved app.%s origin or a dedicated origin outside the public gateway domain", domain)
	}
	return nil
}

// Gateway reserves the whole composer origin for trusted assets. Author content
// must never fall through on this origin, including when the service is disabled.
func Gateway(service *Server, assets fs.FS, fonts fs.FS, publicDomain string, fallback http.Handler) http.Handler {
	origin, _ := url.Parse(service.Origin) // Init validates the configured origin.
	api := service.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v0/mobile/") {
			api.ServeHTTP(w, r)
			return
		}
		requested, err := url.Parse("http://" + r.Host)
		reserved := err == nil && (strings.EqualFold(requested.Hostname(), "app."+publicDomain) || (origin != nil && strings.EqualFold(requested.Hostname(), origin.Hostname())))
		if !reserved {
			fallback.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if origin == nil || !strings.EqualFold(r.Host, origin.Host) || !service.Enabled {
			http.Error(w, "Phone posting is not enabled on this host yet.", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name, ok := MobileAsset(r.URL.Path)
		if ok {
			http.ServeFileFS(w, r, assets, name)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/fonts/") && fonts != nil {
			name := strings.TrimPrefix(r.URL.Path, "/fonts/")
			if name == "SimplonNorm-Regular-WebXL.woff2" || name == "SimplonNorm-Bold-WebXL.woff2" {
				http.ServeFileFS(w, r, fonts, "assets/"+name)
				return
			}
		}
		http.NotFound(w, r)
	})
}

// MobileAsset is a strict allowlist: embedded tests, published site trees and
// future unrelated assets do not become readable at a key-holding origin.
func MobileAsset(path string) (string, bool) {
	if path == "/" {
		return "index.html", true
	}
	switch path {
	case "/index.html", "/app.js", "/style.css", "/protocol.js", "/storage.js", "/pairing.js", "/manifest.webmanifest", "/sw.js", "/icon.svg":
		return path[1:], true
	}
	return "", false
}

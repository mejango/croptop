package server

import (
	"net/http"
	"net/url"
	"slices"
)

// consoleRequestAllowed permits only a trusted console page (or a native
// client with the explicit header) to mutate sensitive console sessions.
func consoleRequestAllowed(r *http.Request, header string, paths ...string) bool {
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
		u, err := url.Parse(ref)
		if err != nil || u.Host != r.Host || !slices.Contains(paths, u.Path) {
			return false
		}
	}
	return r.Header.Get(header) == "1"
}

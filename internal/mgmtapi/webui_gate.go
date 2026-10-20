package mgmtapi

import "net/http"

// webUIOffPaths stay reachable while the web UI is switched off: they are
// not "the UI" but device plumbing. The PAC/WPAD file is what proxy-only
// mode hands to browsers (and an MDM hands to Chrome), the CA cert is what
// every client must install, and /health is a cheap liveness probe.
var webUIOffPaths = map[string]bool{
	"/proxy.pac":   true,
	"/wpad.dat":    true,
	"/wpad.da":     true,
	"/api/ca-cert": true,
	"/health":      true,
}

// webUIGate 404s everything outside webUIOffPaths while WebUIDisabled
// reports true. It runs before authMiddleware so a disabled UI does not
// even redirect to its login page.
func (s *Server) webUIGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.WebUIDisabled != nil && s.WebUIDisabled() && !webUIOffPaths[r.URL.Path] {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

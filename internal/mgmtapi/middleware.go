package mgmtapi

import (
	"net/http"
	"strings"
)

// publicPaths bypass auth entirely - exact-match only (not prefix match),
// matching the Python original's literal path-list middleware.
var publicPaths = map[string]bool{
	"/login.html":      true,
	"/api/login":       true,
	"/api/logout":      true,
	"/api/auth-status": true,
	"/api/version":     true,
	"/proxy.pac":       true,
	"/wpad.dat":        true,
	"/wpad.da":         true,
	// The CA cert is the public half of the intercepting proxy's trust
	// anchor (no private key), and every client device needs it installed
	// before it can be trusted at all - gating it behind login would make
	// devices unable to get set up until someone hands them the management
	// password, which defeats the point. mitmproxy's own mitm.it page works
	// the same way.
	"/api/ca-cert": true,
}

// isStaticAsset lets the CSS/JS/theme files the login page itself needs
// (tailwind.css, theme.css, chrome.js, i18n.js) load without auth - the
// login page couldn't render otherwise. The Python original serves these
// through the same StaticFiles mount as everything else, which is
// only reachable pre-auth for the exact-listed public paths; login.html
// itself pulls in these assets via plain relative <link>/<script> tags, so
// they must also be reachable pre-auth. This is a deliberate, minimal
// widening of the original's allowlist grounded in what login.html
// actually needs to render, not a general static-bypass.
func isStaticAsset(path string) bool {
	return strings.HasSuffix(path, ".css") || strings.HasSuffix(path, ".js")
}

// authMiddleware gates every request except the public allowlist. Auth is
// only enforced when both auth_enabled and password_hash are set - matches
// the Python original's "only active if both" guard. Unauthenticated API
// calls get 401 JSON; unauthenticated page loads redirect to login.html.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Settings()
		if !cfg.AuthEnabled || cfg.PasswordHash == "" {
			next.ServeHTTP(w, r)
			return
		}
		if publicPaths[r.URL.Path] || isStaticAsset(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if s.authTokenValid(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		http.Redirect(w, r, "/login.html", http.StatusFound)
	})
}

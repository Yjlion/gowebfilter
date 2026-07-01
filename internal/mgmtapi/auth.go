package mgmtapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/yjlion/gowebfilter/internal/pwhash"
	"github.com/yjlion/gowebfilter/internal/version"
)

// sessionCookieName matches the Python original exactly - the UI itself
// never reads this cookie directly (it's httpOnly), but the name matters
// for any documentation/tooling that references it.
const sessionCookieName = "wf_session"

const sessionMaxAge = 7 * 24 * 3600 // 7 days, matches the Python original

// sessionToken derives the deterministic session cookie value:
// hex(HMAC-SHA256(secretKey, passwordHash)). This is intentionally NOT a
// random per-session nonce - every session for a given password gets the
// same token, so "logout" is purely a client-side cookie clear (matching
// the Python original: no server-side session store). Changing the
// password changes password_hash, which changes every prior token,
// invalidating all sessions automatically.
func sessionToken(secretKey, passwordHash string) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(passwordHash))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": version.Version})
}

func (s *Server) authTokenValid(r *http.Request) bool {
	cfg := s.Settings()
	if !cfg.AuthEnabled || cfg.PasswordHash == "" {
		return true
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	want := sessionToken(cfg.SecretKey, cfg.PasswordHash)
	return hmac.Equal([]byte(cookie.Value), []byte(want))
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.Settings()
	enabled := cfg.AuthEnabled && cfg.PasswordHash != ""
	writeJSON(w, http.StatusOK, map[string]bool{
		"enabled":       enabled,
		"has_password":  cfg.PasswordHash != "",
		"authenticated": s.authTokenValid(r),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	cfg := s.Settings()
	if cfg.PasswordHash == "" || !cfg.AuthEnabled {
		// No password configured / auth off: login trivially succeeds,
		// matching the Python original.
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if !pwhash.Verify(body.Password, cfg.PasswordHash) {
		writeJSONError(w, http.StatusUnauthorized, "Invalid password")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionToken(cfg.SecretKey, cfg.PasswordHash),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   sessionMaxAge,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

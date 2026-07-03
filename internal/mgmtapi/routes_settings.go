package mgmtapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/pwhash"
	tun "github.com/yjlion/gowebfilter/internal/tun2socks"
)

// settingsOverlay is models.GlobalSettings without its custom
// UnmarshalJSON (Go strips methods when converting to a locally-defined
// named type). Unmarshaling into an *already-populated* value of this type
// gives partial-update semantics: only fields present in the incoming JSON
// are overwritten, everything else keeps its current value - matching the
// Python original's PUT /api/settings, which accepts a partial dict rather
// than requiring the full settings object back.
type settingsOverlay models.GlobalSettings

// toSettingsResponse strips the three secret fields and adds the two
// derived has_* booleans the UI's settings.html expects, matching the
// Python original's response DTO exactly.
func toSettingsResponse(s models.GlobalSettings) map[string]any {
	data, _ := json.Marshal(s)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	delete(m, "password_hash")
	delete(m, "secret_key")
	delete(m, "proxy_auth_password_hash")
	m["has_password"] = s.PasswordHash != ""
	m["has_proxy_auth"] = s.ProxyAuthPasswordHash != ""
	return m
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toSettingsResponse(s.Settings()))
}

func generateSecretKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	cur := s.Settings()
	overlay := settingsOverlay(cur)
	if err := json.Unmarshal(body, &overlay); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid settings body")
		return
	}
	merged := models.GlobalSettings(overlay)

	// Secret fields are never accepted directly from the client - only via
	// the new_password / new_proxy_auth_password hashing path below.
	merged.PasswordHash = cur.PasswordHash
	merged.SecretKey = cur.SecretKey
	merged.ProxyAuthPasswordHash = cur.ProxyAuthPasswordHash

	var extra struct {
		NewPassword          string `json:"new_password"`
		NewProxyAuthPassword string `json:"new_proxy_auth_password"`
	}
	_ = json.Unmarshal(body, &extra)

	if extra.NewPassword != "" {
		h, err := pwhash.Hash(extra.NewPassword)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		merged.PasswordHash = h
		if merged.SecretKey == "" {
			key, err := generateSecretKey()
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "failed to generate secret key")
				return
			}
			merged.SecretKey = key
		}
	}
	if extra.NewProxyAuthPassword != "" {
		h, err := pwhash.Hash(extra.NewProxyAuthPassword)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to hash proxy auth password")
			return
		}
		merged.ProxyAuthPasswordHash = h
	}

	if merged.AuthEnabled && merged.PasswordHash == "" {
		writeJSONError(w, http.StatusBadRequest, "Set a password before enabling authentication.")
		return
	}
	if merged.ProxyAuthEnabled && merged.ProxyAuthPasswordHash == "" {
		writeJSONError(w, http.StatusBadRequest, "Set a proxy auth password before enabling proxy authentication.")
		return
	}
	if err := tun.ValidateConfig(merged.Tun2Socks); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.SaveSettings(merged); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toSettingsResponse(merged))
}

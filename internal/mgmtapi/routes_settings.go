package mgmtapi

import (
	"io"
	"net/http"

	"github.com/yjlion/gowebfilter/internal/settingsvc"
)

// The partial-update merge, secret-field protection, new_password hashing,
// and validation all live in internal/settingsvc so the gomobile native-UI
// path (mobile.UpdateSettingsJson) behaves byte-identically to this handler.

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, settingsvc.SettingsDTO(s.Settings()))
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	merged, err := settingsvc.MergeSettings(s.Settings(), body)
	if err != nil {
		if settingsvc.IsValidationError(err) {
			writeJSONError(w, http.StatusBadRequest, err.Error())
		} else {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	if err := s.SaveSettings(merged); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settingsvc.SettingsDTO(merged))
}

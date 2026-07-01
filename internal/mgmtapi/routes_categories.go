package mgmtapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registerCategoriesRoutes wires GET /api/categories. Returns an empty list
// until internal/categories (Phase 9 of the project plan) lands; the
// policy editor's category checkboxes simply render as an empty set until
// then rather than erroring.
func (s *Server) registerCategoriesRoutes(r chi.Router) {
	r.Get("/api/categories", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"categories": []any{}})
	})
}

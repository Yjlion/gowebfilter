package mgmtapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	imageclassifier "github.com/yjlion/gowebfilter/internal/classify/image"
	"github.com/yjlion/gowebfilter/internal/classify/textbayes"
)

func (s *Server) registerClassifierHealthRoute(r chi.Router) {
	r.Get("/api/tools/classifier-health", s.handleClassifierHealth)
}

func (s *Server) handleClassifierHealth(w http.ResponseWriter, r *http.Request) {
	deep := r.URL.Query().Get("deep") == "1" || strings.EqualFold(r.URL.Query().Get("deep"), "true")
	writeJSON(w, http.StatusOK, map[string]any{
		"text_classifier":  s.textClassifierHealth(deep),
		"image_classifier": imageClassifierHealth(),
	})
}

func (s *Server) textClassifierHealth(deep bool) map[string]any {
	_ = deep
	out := map[string]any{
		"configured": true,
		"model_path": nil,
		"deep_check": deep,
		"ml_enabled": false,
		"fallback":   nil,
		"status":     "load_failed",
	}
	if _, err := textbayes.New(); err != nil {
		out["detail"] = err.Error()
		return out
	}
	out["status"] = "available"
	out["ml_enabled"] = true
	out["detail"] = "embedded Bayesian text classifier loaded successfully"
	return out
}

func imageClassifierHealth() map[string]any {
	if _, err := imageclassifier.New(); err != nil {
		return map[string]any{"status": "load_failed", "available": false, "detail": err.Error()}
	}
	return map[string]any{"status": "available", "available": true, "detail": "embedded image classifier model loaded successfully"}
}

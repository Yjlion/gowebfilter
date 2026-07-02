package mgmtapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	imageclassifier "github.com/yjlion/gowebfilter/internal/classify/image"
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
	modelPath := strings.TrimSpace(s.Settings().TextClassifierModelPath)
	out := map[string]any{
		"configured": modelPath != "",
		"model_path": nilIfEmpty(modelPath),
		"deep_check": deep,
		"ml_enabled": false,
		"fallback":   "keyword_only",
		"status":     "keyword_only",
		"detail":     "text_classifier_model_path is empty; text filtering will use keyword-only fallback",
	}
	if modelPath == "" {
		return out
	}
	if strings.HasSuffix(strings.ToLower(modelPath), ".json") {
		out["status"] = "stale_json_model_path"
		out["detail"] = "text_classifier_model_path points at the old JSON sidecar format; expected a directory containing model.onnx, vocab.txt, and config.json"
		return out
	}
	info, err := os.Stat(modelPath)
	if err != nil {
		out["status"] = "missing_model_dir"
		out["detail"] = err.Error()
		return out
	}
	if !info.IsDir() {
		out["status"] = "model_path_not_directory"
		out["detail"] = "text_classifier_model_path must be a directory containing model.onnx, vocab.txt, and config.json"
		return out
	}
	missing := missingTextModelFiles(modelPath)
	if len(missing) > 0 {
		out["status"] = "missing_model_files"
		out["missing_files"] = missing
		out["detail"] = "text model directory is missing required files"
		return out
	}
	if !deep {
		out["status"] = "model_files_present"
		out["detail"] = "required model files are present; proxy startup can load the model to verify ONNX Runtime"
		return out
	}
	return textClassifierDeepHealth(modelPath, out)
}

func missingTextModelFiles(dir string) []string {
	required := []string{"model.onnx", "vocab.txt", "config.json"}
	missing := []string{}
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			missing = append(missing, name)
		}
	}
	return missing
}

func imageClassifierHealth() map[string]any {
	if _, err := imageclassifier.New(); err != nil {
		return map[string]any{"status": "load_failed", "available": false, "detail": err.Error()}
	}
	return map[string]any{"status": "available", "available": true, "detail": "embedded image classifier model loaded successfully"}
}

//go:build cgo

package mgmtapi

import (
	"strings"

	"github.com/yjlion/gowebfilter/internal/classify/text"
)

func textClassifierDeepHealth(modelPath string, out map[string]any) map[string]any {
	if _, err := text.Load(modelPath); err != nil {
		out["status"] = "load_failed"
		out["detail"] = err.Error()
		if strings.Contains(err.Error(), "API version") || strings.Contains(strings.ToLower(err.Error()), "onnxruntime") {
			out["hint"] = "check ONNXRUNTIME_SHARED_LIBRARY or place the packaged ONNX Runtime shared library next to webfilter"
		}
		return out
	}
	out["status"] = "ml_active"
	out["ml_enabled"] = true
	out["fallback"] = nil
	out["detail"] = "text classifier ML model loaded successfully"
	return out
}

//go:build !cgo

package mgmtapi

func textClassifierDeepHealth(_ string, out map[string]any) map[string]any {
	out["status"] = "model_files_present"
	out["detail"] = "required model files are present; rebuild with CGO_ENABLED=1 to load ONNX Runtime and verify the ML stage"
	out["hint"] = "this project requires CGO for the text classifier; without it the management API can only perform structural file checks"
	return out
}

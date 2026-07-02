// Package onnxrt initializes onnxruntime_go's process-global environment
// exactly once, shared by internal/classify/image and internal/classify/text
// (both are unconditionally CGO/onnxruntime-backed - see CLAUDE.md).
package onnxrt

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var (
	initOnce sync.Once
	initErr  error
)

// EnsureEnvironment initializes onnxruntime's global environment exactly
// once per process, safe to call from both the image and text classifier
// packages regardless of which loads first. ONNXRUNTIME_SHARED_LIBRARY, if
// set, points at the onnxruntime.dll/.so/.dylib to dynamically load
// (onnxruntime_go does not link against it at compile time); otherwise this
// looks for a platform-appropriate shared library sitting next to the
// running executable (how release archives bundle it - see
// scripts/package-release.sh), falling back to onnxruntime_go's own
// platform-specific default search path if neither is found.
func EnsureEnvironment() error {
	initOnce.Do(func() {
		if libPath := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY"); libPath != "" {
			ort.SetSharedLibraryPath(libPath)
		} else if libPath, ok := findSharedLibraryNextToExecutable(); ok {
			ort.SetSharedLibraryPath(libPath)
		}
		initErr = ort.InitializeEnvironment()
	})
	return initErr
}

// executableName is platform's default onnxruntime shared library file
// name, matching what scripts/package-release.sh bundles alongside the
// webfilter binary in each release archive.
func executableName() string {
	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll"
	case "darwin":
		return "libonnxruntime.dylib"
	default:
		return "libonnxruntime.so"
	}
}

// findSharedLibraryNextToExecutable looks for the onnxruntime shared
// library in the same directory as the running binary, so a packaged
// release works without requiring the operator to set
// ONNXRUNTIME_SHARED_LIBRARY by hand.
func findSharedLibraryNextToExecutable() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", false
	}
	candidate := filepath.Join(filepath.Dir(exe), executableName())
	if _, err := os.Stat(candidate); err != nil {
		return "", false
	}
	return candidate, true
}

package tun2socks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// tun2socks runs as a separate process rather than as a linked-in library.
//
// The upstream project (https://tun2socks.com, github.com/xjasonlyu/tun2socks)
// needs root/Administrator to create a TUN device and rewrite the routing
// table. Linking it in meant *the whole filter* - MITM engine, management API,
// SQLite log store - had to run elevated, and the setup differed per platform.
// Supervising the official prebuilt binary confines the privileged, OS-coupled
// part to a small process we can start, stop, and watch die.
//
// The Android port is unaffected: mobile/tun_capture.go still drives the
// library in-process from the VpnService file descriptor, which needs no
// elevation because Android grants the TUN fd to the app.

// binaryName is the executable this package supervises.
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "tun2socks.exe"
	}
	return "tun2socks"
}

// InstallDir returns the directory the downloader installs into and Resolve
// looks in first: a "bin" directory beside the running webfilter executable.
// Keeping the two side by side means a relocated install (a USB stick, a
// container image, an unpacked release archive) carries its tun2socks with it.
func InstallDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "bin"), nil
}

// ErrBinaryNotFound is returned by Resolve when no tun2socks executable is
// installed. Callers surface it as a "download it" prompt rather than a crash.
var ErrBinaryNotFound = errors.New("tun2socks binary not found")

// Resolve locates the tun2socks executable, preferring the copy installed
// beside webfilter over one on PATH, so a downloaded binary always wins over
// whatever the distro happens to ship. Returns the path and where it came from
// ("downloaded" or "path").
func Resolve() (path, source string, err error) {
	if dir, err := InstallDir(); err == nil {
		candidate := filepath.Join(dir, binaryName())
		if isExecutableFile(candidate) {
			return candidate, "downloaded", nil
		}
	}
	if p, err := exec.LookPath(binaryName()); err == nil {
		return p, "path", nil
	}
	return "", "", ErrBinaryNotFound
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	// Windows has no execute bit; presence is enough there.
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

// versionTimeout bounds the `tun2socks --version` probe. It is a local process
// printing one line, so anything slower than this means something is wrong and
// we would rather report an unknown version than stall a status request.
const versionTimeout = 3 * time.Second

// Version runs the binary's --version and returns the first line, or "" if it
// can't be determined. A missing version is informational only - it never
// blocks starting the process.
func Version(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line)
}

// installMetaName records what the downloader installed, next to the binary
// itself, so the UI can report provenance (version + checksum + when) without
// re-running the binary or re-querying GitHub.
const installMetaName = "tun2socks.json"

// InstallMeta is the provenance record written beside a downloaded binary.
type InstallMeta struct {
	Version    string `json:"version"`
	Asset      string `json:"asset"`
	SHA256     string `json:"sha256"`
	Downloaded string `json:"downloaded"`
}

// readInstallMeta loads the provenance record for a downloaded binary. A
// missing or unreadable file is not an error: a binary found on PATH, or one
// dropped in by hand, simply has no provenance to report.
func readInstallMeta(binPath string) (InstallMeta, bool) {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(binPath), installMetaName))
	if err != nil {
		return InstallMeta{}, false
	}
	var meta InstallMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return InstallMeta{}, false
	}
	return meta, true
}

func writeInstallMeta(dir string, meta InstallMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, installMetaName), data, 0o644)
}

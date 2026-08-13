package tun2socks

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The asset name is the one thing that has to match upstream exactly, and it
// differs from Go's own naming for ARM.
func TestAssetName(t *testing.T) {
	for _, tc := range []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "tun2socks-linux-amd64.zip"},
		{"linux", "arm64", "tun2socks-linux-arm64.zip"},
		{"windows", "amd64", "tun2socks-windows-amd64.zip"},
		{"darwin", "arm64", "tun2socks-darwin-arm64.zip"},
		// Upstream has no bare "arm" asset; GOARM picks the variant.
		{"linux", "arm", "tun2socks-linux-armv7.zip"},
	} {
		if got := AssetName(tc.goos, tc.goarch); got != tc.want {
			t.Errorf("AssetName(%q, %q) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}

	t.Setenv("GOARM", "6")
	if got := AssetName("linux", "arm"); got != "tun2socks-linux-armv6.zip" {
		t.Errorf("AssetName with GOARM=6 = %q, want tun2socks-linux-armv6.zip", got)
	}
}

// TestDownloadInstallsAndVerifies covers the whole install path against a fake
// release server: asset selection, checksum verification, extraction, and the
// provenance record.
func TestDownloadInstallsAndVerifies(t *testing.T) {
	const payload = "#!/bin/sh\necho fake tun2socks\n"
	srv := fakeReleaseServer(t, "v9.9.9", payload, true)
	defer srv.Close()

	dest := t.TempDir()
	meta, err := Download(context.Background(), dest, srv.URL+"/release")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	if meta.Version != "v9.9.9" {
		t.Errorf("version = %q, want v9.9.9", meta.Version)
	}
	if meta.Asset != AssetName(runtime.GOOS, runtime.GOARCH) {
		t.Errorf("asset = %q, want the running platform's", meta.Asset)
	}
	if meta.Downloaded == "" {
		t.Error("downloaded timestamp is empty")
	}

	binPath := filepath.Join(dest, binaryName())
	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if string(got) != payload {
		t.Errorf("installed binary = %q, want %q", got, payload)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(binPath)
		if err != nil {
			t.Fatal(err)
		}
		// Resolve (and the supervisor) require the execute bit.
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("installed binary mode = %v, want it executable", info.Mode().Perm())
		}
	}

	// Provenance must land beside the binary so status can report it.
	raw, err := os.ReadFile(filepath.Join(dest, installMetaName))
	if err != nil {
		t.Fatalf("read install metadata: %v", err)
	}
	var onDisk InstallMeta
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("parse install metadata: %v", err)
	}
	if onDisk != meta {
		t.Errorf("metadata on disk = %+v, want %+v", onDisk, meta)
	}

	// No staging directories left behind.
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".dl-") {
			t.Errorf("staging dir %q was left behind", e.Name())
		}
	}
}

// TestDownloadRejectsChecksumMismatch: the digest is the only thing standing
// between a compromised mirror and an executable running as root, so a mismatch
// must abort before anything is installed.
func TestDownloadRejectsChecksumMismatch(t *testing.T) {
	srv := fakeReleaseServer(t, "v9.9.9", "payload", false)
	defer srv.Close()

	dest := t.TempDir()
	_, err := Download(context.Background(), dest, srv.URL+"/release")
	if err == nil {
		t.Fatal("Download() succeeded despite a bad checksum")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want a checksum mismatch", err)
	}
	if _, err := os.Stat(filepath.Join(dest, binaryName())); !os.IsNotExist(err) {
		t.Error("a binary was installed despite the checksum mismatch")
	}
}

func TestDownloadReportsRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := Download(context.Background(), t.TempDir(), srv.URL+"/release")
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("error = %v, want it to name the GitHub rate limit", err)
	}
}

func TestDownloadReportsMissingAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, release{TagName: "v9.9.9", Assets: []releaseAsset{
			{Name: "tun2socks-plan9-mips.zip", BrowserDownloadURL: "http://example.invalid/x.zip"},
		}})
	}))
	defer srv.Close()

	_, err := Download(context.Background(), t.TempDir(), srv.URL+"/release")
	if err == nil || !strings.Contains(err.Error(), "has no asset") {
		t.Errorf("error = %v, want it to report the missing asset", err)
	}
}

// TestExtractBinaryAcceptsAssetNamedEntry pins the real-world archive layout:
// upstream names the entry after the asset ("tun2socks-linux-amd64"), not
// "tun2socks", which an earlier exact-match implementation choked on.
func TestExtractBinaryAcceptsAssetNamedEntry(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "a.zip")
	if err := os.WriteFile(zipPath, buildZip(t, map[string]string{"tun2socks-linux-amd64": "body"}), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "out")
	if err := extractBinary(zipPath, dest); err != nil {
		t.Fatalf("extractBinary() error = %v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "body" {
		t.Errorf("extracted %q, want %q", got, "body")
	}
}

// An archive holding several tun2socks-ish entries is ambiguous; guessing could
// install the wrong payload, so it must be refused.
func TestExtractBinaryRefusesAmbiguousArchive(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "a.zip")
	if err := os.WriteFile(zipPath, buildZip(t, map[string]string{
		"tun2socks-linux-amd64": "a",
		"tun2socks-extra":       "b",
	}), 0o644); err != nil {
		t.Fatal(err)
	}

	err := extractBinary(zipPath, filepath.Join(dir, "out"))
	if err == nil || !strings.Contains(err.Error(), "refusing to guess") {
		t.Errorf("error = %v, want a refusal to guess", err)
	}
}

func TestExtractBinaryRejectsArchiveWithoutBinary(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "a.zip")
	if err := os.WriteFile(zipPath, buildZip(t, map[string]string{"README.md": "hi"}), 0o644); err != nil {
		t.Fatal(err)
	}

	err := extractBinary(zipPath, filepath.Join(dir, "out"))
	if err == nil || !strings.Contains(err.Error(), "no tun2socks executable") {
		t.Errorf("error = %v, want a missing-executable error", err)
	}
}

// fakeReleaseServer serves a GitHub-shaped release whose asset is a zip holding
// payload. When goodDigest is false the advertised digest is wrong, exercising
// the verification path.
func fakeReleaseServer(t *testing.T, tag, payload string, goodDigest bool) *httptest.Server {
	t.Helper()
	assetName := AssetName(runtime.GOOS, runtime.GOARCH)
	// Name the entry the way upstream does, so the test exercises the real layout.
	zipped := buildZip(t, map[string]string{strings.TrimSuffix(assetName, ".zip"): payload})

	sum := sha256.Sum256(zipped)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if !goodDigest {
		digest = "sha256:" + strings.Repeat("00", sha256.Size)
	}

	mux := http.NewServeMux()
	srv := httptest.NewUnstartedServer(mux)
	mux.HandleFunc("/asset.zip", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipped)
	})
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, release{TagName: tag, Assets: []releaseAsset{{
			Name:               assetName,
			BrowserDownloadURL: "http://" + srv.Listener.Addr().String() + "/asset.zip",
			Digest:             digest,
		}}})
	})
	srv.Start()
	return srv
}

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

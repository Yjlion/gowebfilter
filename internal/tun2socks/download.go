package tun2socks

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DefaultReleaseAPI is the GitHub release endpoint for the upstream project
// behind https://tun2socks.com. Its per-asset "digest" field carries a
// sha256:... checksum, so no separate checksums file is needed.
const DefaultReleaseAPI = "https://api.github.com/repos/xjasonlyu/tun2socks/releases/latest"

// maxAssetBytes caps the download. Real assets are ~5 MB; 64 MB leaves room for
// growth while bounding a misbehaving or hostile server.
const maxAssetBytes = 64 << 20

// downloadTimeout bounds the whole fetch-and-install cycle.
const downloadTimeout = 10 * time.Minute

// downloadMu serializes installs so two concurrent callers (the API button and
// the CLI, say) can't both stage into the same directory and race on the final
// rename. Mirrors internal/categories' indexMu.
var downloadMu sync.Mutex

// Download fetches the tun2socks release asset matching the running platform,
// verifies its checksum, and installs the executable into destDir (normally
// InstallDir()). It returns what it installed.
//
// The install is staged in a temp directory and moved into place at the end, so
// an interrupted download can never leave a half-written executable that a
// later Start would try to run.
func Download(ctx context.Context, destDir, apiURL string) (InstallMeta, error) {
	downloadMu.Lock()
	defer downloadMu.Unlock()

	if apiURL == "" {
		apiURL = DefaultReleaseAPI
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	rel, err := fetchRelease(ctx, apiURL)
	if err != nil {
		return InstallMeta{}, err
	}
	wantAsset := AssetName(runtime.GOOS, runtime.GOARCH)
	asset, ok := rel.find(wantAsset)
	if !ok {
		return InstallMeta{}, fmt.Errorf("tun2socks release %s has no asset %q for %s/%s",
			rel.TagName, wantAsset, runtime.GOOS, runtime.GOARCH)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return InstallMeta{}, fmt.Errorf("create install dir: %w", err)
	}
	stageDir, err := os.MkdirTemp(destDir, ".dl-*")
	if err != nil {
		return InstallMeta{}, fmt.Errorf("create staging dir: %w", err)
	}
	defer os.RemoveAll(stageDir)

	zipPath := filepath.Join(stageDir, "asset.zip")
	sum, err := downloadTo(ctx, asset.BrowserDownloadURL, zipPath)
	if err != nil {
		return InstallMeta{}, err
	}
	if want := strings.TrimPrefix(asset.Digest, "sha256:"); want != "" && !strings.EqualFold(want, sum) {
		return InstallMeta{}, fmt.Errorf("tun2socks %s checksum mismatch: got %s, want %s", wantAsset, sum, want)
	}

	stagedBin := filepath.Join(stageDir, binaryName())
	if err := extractBinary(zipPath, stagedBin); err != nil {
		return InstallMeta{}, err
	}

	meta := InstallMeta{
		Version:    rel.TagName,
		Asset:      wantAsset,
		SHA256:     sum,
		Downloaded: time.Now().UTC().Format(time.RFC3339),
	}
	// Write provenance before the binary lands, so a binary that exists always
	// has its record beside it rather than the other way round.
	if err := writeInstallMeta(stageDir, meta); err != nil {
		return InstallMeta{}, fmt.Errorf("write install metadata: %w", err)
	}
	if err := os.Rename(filepath.Join(stageDir, installMetaName), filepath.Join(destDir, installMetaName)); err != nil {
		return InstallMeta{}, fmt.Errorf("install metadata: %w", err)
	}
	if err := os.Rename(stagedBin, filepath.Join(destDir, binaryName())); err != nil {
		return InstallMeta{}, fmt.Errorf("install tun2socks: %w", err)
	}
	return meta, nil
}

// AssetName is the release asset for a GOOS/GOARCH pair. Upstream publishes
// tun2socks-<os>-<arch>.zip, with ARM spelled armv5/armv6/armv7 rather than Go's
// bare "arm"; GOARM selects which, defaulting to v7 (Go's own default for
// modern ARM targets, and the only variant a desktop ARM board realistically
// wants).
func AssetName(goos, goarch string) string {
	arch := goarch
	if arch == "arm" {
		arch = "armv7"
		switch os.Getenv("GOARM") {
		case "5":
			arch = "armv5"
		case "6":
			arch = "armv6"
		}
	}
	return fmt.Sprintf("tun2socks-%s-%s.zip", goos, arch)
}

// release is the slice of GitHub's release JSON this package needs.
type release struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	// Digest is GitHub's per-asset checksum, formatted "sha256:<hex>".
	Digest string `json:"digest"`
}

func (r release) find(name string) (releaseAsset, bool) {
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return releaseAsset{}, false
}

func fetchRelease(ctx context.Context, apiURL string) (release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("query tun2socks releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		// Unauthenticated GitHub API calls are rate limited per IP, which is a
		// transient, self-inflicted condition worth naming precisely.
		return release{}, fmt.Errorf("query tun2socks releases: GitHub API rate limit reached (HTTP %d); retry later or install the binary manually", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("query tun2socks releases: HTTP %d", resp.StatusCode)
	}

	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return release{}, fmt.Errorf("decode tun2socks release list: %w", err)
	}
	return rel, nil
}

// downloadTo streams url into path and returns the payload's hex SHA-256,
// hashing as it writes so the bytes are never held in memory twice.
func downloadTo(ctx context.Context, url, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	if n > maxAssetBytes {
		return "", fmt.Errorf("download %s: exceeds %d MB cap", url, maxAssetBytes>>20)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractBinary pulls the tun2socks executable out of the release zip and
// writes it to destPath.
//
// The entry is not simply named "tun2socks": upstream ships it under the asset
// name ("tun2socks-linux-amd64", "tun2socks-windows-amd64.exe"), and that
// spelling has changed across releases. Rather than hardcode one, accept any
// entry whose base name starts with "tun2socks" and require exactly one match,
// so a renamed entry keeps working while an archive carrying extra payloads is
// refused rather than silently unpacked.
func extractBinary(zipPath, destPath string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open tun2socks archive: %w", err)
	}
	defer zr.Close()

	var candidates []*zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if strings.HasPrefix(strings.ToLower(filepath.Base(f.Name)), "tun2socks") {
			candidates = append(candidates, f)
		}
	}
	switch len(candidates) {
	case 1:
	case 0:
		return errors.New("tun2socks archive contains no tun2socks executable")
	default:
		return fmt.Errorf("tun2socks archive contains %d tun2socks entries; refusing to guess", len(candidates))
	}

	rc, err := candidates[0].Open()
	if err != nil {
		return fmt.Errorf("read %s from archive: %w", candidates[0].Name, err)
	}
	defer rc.Close()

	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, io.LimitReader(rc, maxAssetBytes)); err != nil {
		return fmt.Errorf("extract %s: %w", candidates[0].Name, err)
	}
	return out.Close()
}

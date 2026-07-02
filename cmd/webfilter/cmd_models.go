package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/yjlion/gowebfilter/internal/classify/image"
	"github.com/yjlion/gowebfilter/internal/config"
)

// defaultImageModelURL is NudeNet v3's official "320n.onnx" release asset -
// see the AGPLv3 notice printed by runModelsDownload before it downloads
// anything. This is a separately-licensed third-party artifact, not
// covered by this project's own license; it is never vendored into this
// repo or bundled into release archives.
const defaultImageModelURL = "https://github.com/notAI-tech/NudeNet/releases/download/v3.4-weights/320n.onnx"

// imageModelSHA256 pins the expected checksum of the file at
// defaultImageModelURL, verified after download so a corrupted or tampered
// mirror fails loudly instead of silently loading into onnxruntime. Must be
// filled in from an environment with real access to github.com's asset CDN
// (e.g. `curl -sL <defaultImageModelURL> | sha256sum`) - this dev sandbox's
// outbound network cannot reach it directly (confirmed: a direct request
// returns an HTML sign-in page, not the binary), so it is deliberately left
// unpinned here rather than filled with a fabricated value.
const imageModelSHA256 = ""

const agplNotice = `NudeNet v3 ("320n.onnx") is licensed under the GNU Affero General Public
License v3 (AGPLv3) by notAI-tech - see https://github.com/notAI-tech/NudeNet.
This is a separate, third-party license from this project's own; downloading
these weights is a deliberate, explicit opt-in and they are not bundled into
this repo or any release archive. Review the AGPLv3's terms (including its
network-use provisions) before enabling image classification in production.`

func newModelsCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "models",
		Short: "Manage NSFW-detection ML models",
	}
	download := &cobra.Command{
		Use:   "download",
		Short: "Download the NudeNet v3 ONNX image-classification model (AGPLv3-licensed)",
	}
	f := addConfigFlags(download)
	var url, dir string
	download.Flags().StringVar(&url, "url", defaultImageModelURL,
		"source .onnx URL")
	download.Flags().StringVar(&dir, "dir", "",
		"destination directory (default: image_classifier_model_path's directory, or ./models/image-nsfw)")
	download.RunE = func(cmd *cobra.Command, args []string) error {
		return runModelsDownload(f.settingsPath, url, dir)
	}
	root.AddCommand(download)
	return root
}

func runModelsDownload(settingsPath, url, dir string) error {
	fmt.Println(agplNotice)
	fmt.Println()

	if dir == "" {
		settings, err := config.LoadSettings(settingsPath)
		if err == nil && settings.ImageClassifierModelPath != "" {
			dir = filepath.Dir(settings.ImageClassifierModelPath)
		} else {
			dir = "./models/image-nsfw"
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	modelPath := filepath.Join(dir, "320n.onnx")
	fmt.Printf("[models] downloading %s ...\n", url)
	if err := downloadFile(url, modelPath); err != nil {
		return fmt.Errorf("download model: %w", err)
	}

	labelsPath := filepath.Join(dir, "320n.labels.json")
	labelsJSON, err := json.MarshalIndent(image.NudeNetV3Labels, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}
	if err := os.WriteFile(labelsPath, labelsJSON, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", labelsPath, err)
	}

	noticePath := filepath.Join(dir, "LICENSE-NOTICE.txt")
	if err := os.WriteFile(noticePath, []byte(agplNotice+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", noticePath, err)
	}

	fmt.Printf("[models] wrote %s\n", modelPath)
	fmt.Printf("[models] wrote %s (%d classes)\n", labelsPath, len(image.NudeNetV3Labels))
	fmt.Printf("[models] wrote %s\n", noticePath)
	fmt.Printf("[models] done - set image_classifier_model_path to %s in settings.json (and enable\n", modelPath)
	fmt.Println("[models] image_classifier per-policy) to start using it; this command does not modify settings itself.")
	return nil
}

// downloadFile streams url to a temporary file alongside destPath and
// renames it into place only on success (and after checksum verification,
// if imageModelSHA256 is set), so a failed or corrupted download never
// leaves a file that a later New() call would fail to parse cryptically.
func downloadFile(url, destPath string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	// GitHub's release-asset CDN has been observed serving an HTML sign-in
	// page (HTTP 200, not an error status) instead of the binary asset for
	// some requests - catch that here instead of silently writing the
	// error page to disk as if it were the model.
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "text/html") {
		return fmt.Errorf("response Content-Type is %q, not a binary download - the source may be rate-limiting, redirecting to a sign-in page, or otherwise not serving the asset directly; retry, or download manually and place it at %s", ct, destPath)
	}

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath) // no-op once renamed below

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if imageModelSHA256 != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != imageModelSHA256 {
			return fmt.Errorf("checksum mismatch: got %s, want %s (possible corrupted download or tampered mirror)",
				got, imageModelSHA256)
		}
	}

	return os.Rename(tmpPath, destPath)
}

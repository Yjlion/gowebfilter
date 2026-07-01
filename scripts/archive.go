//go:build ignore

// Command archive packages a directory into a .tar.gz or .zip archive.
// Used by scripts/package-release.sh instead of shelling out to a host
// `tar`/`zip` binary, so packaging works identically on any machine that
// already has the Go toolchain (including plain git-bash on Windows, which
// has tar but not zip).
//
// Usage: go run archive.go <src-dir> <archive-path.tar.gz|.zip>
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: go run archive.go <src-dir> <archive-path.tar.gz|.zip>")
		os.Exit(2)
	}
	srcDir, archivePath := os.Args[1], os.Args[2]

	var err error
	switch {
	case strings.HasSuffix(archivePath, ".tar.gz"):
		err = writeTarGz(srcDir, archivePath)
	case strings.HasSuffix(archivePath, ".zip"):
		err = writeZip(srcDir, archivePath)
	default:
		err = fmt.Errorf("unsupported archive extension: %s (want .tar.gz or .zip)", archivePath)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// relName returns path's slash-separated name relative to srcDir's parent,
// so every entry in the archive is prefixed with srcDir's own base name
// (e.g. "webfilter-v1.0.0-linux-amd64/webfilter") - the conventional layout
// for a release tarball that extracts into its own directory.
func relName(srcDir, path string) (string, error) {
	rel, err := filepath.Rel(filepath.Dir(srcDir), path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func writeTarGz(srcDir, archivePath string) error {
	f, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, err := relName(srcDir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		if d.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
}

func writeZip(srcDir, archivePath string) error {
	f, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name, err := relName(srcDir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = name
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(w, in)
		return err
	})
}

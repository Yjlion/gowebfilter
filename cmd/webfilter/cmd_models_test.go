package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadFileRejectsHTMLResponse(t *testing.T) {
	// Regression test: a source that serves an HTML sign-in/error page with
	// HTTP 200 (observed in practice against a real release-asset URL) must
	// not be silently written to disk as if it were the binary model.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<!DOCTYPE html><html><head><title>Sign in to GitHub</title></head></html>"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "model.onnx")
	if err := downloadFile(srv.URL, dest); err == nil {
		t.Fatal("downloadFile() with an HTML response should fail, got nil error")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("downloadFile() with an HTML response should not leave a file at dest")
	}
	if _, err := os.Stat(dest + ".tmp"); err == nil {
		t.Fatal("downloadFile() should clean up its .tmp file on failure")
	}
}

func TestDownloadFileAcceptsBinaryResponse(t *testing.T) {
	body := []byte{0x08, 0x01, 0x12, 0x00, 0x42, 0xff} // arbitrary non-text bytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "model.onnx")
	if err := downloadFile(srv.URL, dest); err != nil {
		t.Fatalf("downloadFile() error = %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("downloadFile() wrote %v, want %v", got, body)
	}
}

func TestDownloadFileRejectsNon200Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "model.onnx")
	if err := downloadFile(srv.URL, dest); err == nil {
		t.Fatal("downloadFile() with a 404 response should fail, got nil error")
	}
}

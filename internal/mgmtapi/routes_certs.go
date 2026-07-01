package mgmtapi

import (
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/yjlion/gowebfilter/internal/certs"
)

const maxCertImportBytes = 256 * 1024

func (s *Server) registerCertsRoutes(r chi.Router) {
	r.Get("/api/ca-cert", s.handleCACertDownload)
	r.Get("/api/certs/export", s.handleCertsExport)
	r.Post("/api/certs/import", s.handleCertsImport)
}

// handleCACertDownload serves the public CA certificate only - what a
// client device installs to trust the intercepting proxy. Filename/media
// type match the Python original's mitmproxy-ca-cert.cer download.
func (s *Server) handleCACertDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="webfilter-ca-cert.pem"`)
	_, _ = w.Write(s.CA.CertPEM)
}

// handleCertsExport serves the full CA bundle (certificate + private key)
// for migrating the same CA to another instance - keep this endpoint
// auth-gated in production; unlike /api/ca-cert it discloses the private
// key.
func (s *Server) handleCertsExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="webfilter-ca.pem"`)
	_, _ = w.Write(s.CA.BundlePEM())
}

func (s *Server) handleCertsImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCertImportBytes)
	if err := r.ParseMultipartForm(maxCertImportBytes); err != nil {
		writeJSONError(w, http.StatusBadRequest, "File too large or not a valid upload.")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Missing \"file\" field.")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxCertImportBytes+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Failed to read upload.")
		return
	}
	if len(data) > maxCertImportBytes {
		writeJSONError(w, http.StatusBadRequest, "File too large for a CA bundle.")
		return
	}

	newCA, err := certs.ImportBundle(s.Settings().CertDir, data)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid CA bundle: %v", err))
		return
	}
	s.CA = newCA
	if s.OnCARotated != nil {
		s.OnCARotated()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"note": "CA imported. Restart the proxy for the new CA to take full effect.",
	})
}

// Package mgmtapi implements the management HTTP server: the REST API
// under /api/* plus the embedded Tailwind/Alpine web UI, matching the
// Python original's FastAPI app's endpoint paths and JSON shapes exactly
// so the UI files can be reused unmodified.
package mgmtapi

import (
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yjlion/gowebfilter/internal/certs"
	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/logstore"
	"github.com/yjlion/gowebfilter/internal/models"
)

// Server holds everything the API routes need. Settings are cached
// in-memory (settingsMu-guarded) and refreshed on every read/write through
// this Server so the management API and the file on disk never drift
// within a single process - structural settings (paths, ports) still need
// a process restart to take effect, matching the Python original (its own
// backup-restore endpoint returns the same "restart to take full effect"
// note).
type Server struct {
	SettingsPath string
	Policies     *config.PolicyStore
	Logs         *logstore.Store
	CA           *certs.CA
	StartedAt    time.Time

	// OnCARotated is invoked after a successful CA import so the proxy
	// engine (Phase 4/5, when co-located in the same process via `run`)
	// can evict its leaf-certificate cache. nil (the default, e.g. under
	// standalone `mgmt`) is a valid no-op.
	OnCARotated func()

	settingsMu sync.RWMutex
	settings   models.GlobalSettings
}

// NewServer loads settings.json once and wires up the policy store, log
// store, and CA rooted at whatever directories that settings file
// specifies.
func NewServer(settingsPath string) (*Server, error) {
	s, err := config.LoadSettings(settingsPath)
	if err != nil {
		return nil, err
	}
	logs, err := logstore.Configure(s.DBPath(), s.LogRetentionDays, s.LogRequests, s.LogBlocks)
	if err != nil {
		return nil, err
	}
	ca, err := certs.LoadOrCreateCA(s.CertDir)
	if err != nil {
		return nil, err
	}
	return &Server{
		SettingsPath: settingsPath,
		Policies:     config.NewPolicyStore(s.PoliciesDir),
		Logs:         logs,
		CA:           ca,
		StartedAt:    time.Now(),
		settings:     s,
	}, nil
}

// Settings returns the current in-memory settings snapshot.
func (s *Server) Settings() models.GlobalSettings {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.settings
}

// SaveSettings persists newSettings to disk and updates the in-memory
// cache atomically with respect to concurrent readers.
func (s *Server) SaveSettings(newSettings models.GlobalSettings) error {
	if err := config.SaveSettings(s.SettingsPath, newSettings); err != nil {
		return err
	}
	s.settingsMu.Lock()
	s.settings = newSettings
	s.settingsMu.Unlock()
	return nil
}

// Router assembles the full chi router: public paths, the auth middleware
// gate, every /api/* route, PAC/WPAD, and the embedded static UI.
func (s *Server) Router() *chi.Mux {
	r := chi.NewRouter()
	r.Use(s.authMiddleware)

	r.Get("/api/version", s.handleVersion)
	r.Get("/api/auth-status", s.handleAuthStatus)
	r.Post("/api/login", s.handleLogin)
	r.Post("/api/logout", s.handleLogout)

	r.Get("/api/status", s.handleStatus)

	r.Get("/api/policies", s.handleListPolicies)
	r.Post("/api/policies", s.handleCreatePolicy)
	r.Get("/api/policies/{name}", s.handleGetPolicy)
	r.Put("/api/policies/{name}", s.handleUpdatePolicy)
	r.Delete("/api/policies/{name}", s.handleDeletePolicy)

	r.Get("/api/settings", s.handleGetSettings)
	r.Put("/api/settings", s.handleUpdateSettings)

	r.Get("/api/logs", s.handleLogs)
	r.Get("/api/analytics", s.handleAnalytics)

	r.Get("/proxy.pac", s.handlePAC)
	r.Get("/wpad.dat", s.handlePAC)
	r.Get("/wpad.da", s.handlePAC)

	r.Get("/api/wireguard", s.handleWireguardStub)
	r.Post("/api/wireguard", s.handleWireguardStub)

	s.registerCertsRoutes(r)
	s.registerCategoriesRoutes(r)
	s.registerBackupRoutes(r)
	s.registerToolsRoutes(r)
	s.registerLogsExportRoute(r)

	// Unmatched paths (including unknown /api/* paths) fall through to the
	// static handler, which returns the same {"detail":"Not Found"} JSON
	// FastAPI's default 404 handler produces (verified against the live
	// Python server) whether or not the path happens to look like an API
	// route.
	r.NotFound(staticHandler().ServeHTTP)
	r.Handle("/*", staticHandler())
	return r
}

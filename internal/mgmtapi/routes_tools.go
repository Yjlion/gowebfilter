package mgmtapi

import "github.com/go-chi/chi/v5"

// registerToolsRoutes wires /api/tools/{scan,youtube,doh,public-ip,
// neighbors} once the classifiers (Phase 7/8) and internal/neighbors
// (Phase 9) they depend on exist.
func (s *Server) registerToolsRoutes(r chi.Router) {}

// registerLogsExportRoute wires GET /api/logs/export (CSV/XLSX streaming
// download), landing alongside the rest of Phase 9's remaining endpoints.
func (s *Server) registerLogsExportRoute(r chi.Router) {}

package mgmtapi

import "github.com/go-chi/chi/v5"

// registerBackupRoutes wires GET/POST /api/backup once the atomic
// export/import bundle logic (Phase 9 of the project plan) lands.
func (s *Server) registerBackupRoutes(r chi.Router) {}

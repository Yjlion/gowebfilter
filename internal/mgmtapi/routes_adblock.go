package mgmtapi

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yjlion/gowebfilter/internal/adblock"
)

// registerAdblockRoutes wires the shared adblock list store:
//
//	GET    /api/adblock/lists               catalog + install status
//	POST   /api/adblock/lists/{name}/update download (or refresh) one list
//	POST   /api/adblock/update              refresh every installed list
//	DELETE /api/adblock/lists/{name}        remove a downloaded list
//
// The proxy engine picks up downloaded files by mtime, so this works
// whether the engine shares the process or not. Downloads go through the
// engine's egress dialer (set up in NewServer) so they are never captured
// by TUN/gateway mode on the same host.
func (s *Server) registerAdblockRoutes(r chi.Router) {
	r.Get("/api/adblock/lists", func(w http.ResponseWriter, r *http.Request) {
		st := s.adblockStore()
		writeJSON(w, http.StatusOK, map[string]any{
			"lists":         st.Status(),
			"default_lists": adblock.DefaultLists,
			"dir":           st.Dir(),
		})
	})
	r.With(s.requireUnlocked).Post("/api/adblock/lists/{name}/update", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		meta, err := s.adblockStore().Download(ctx, chi.URLParam(r, "name"))
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, meta)
	})
	r.With(s.requireUnlocked).Post("/api/adblock/update", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		st := s.adblockStore()
		results := map[string]string{}
		for _, m := range st.Status() {
			if !m.Installed {
				continue
			}
			if _, err := st.Download(ctx, m.Name); err != nil {
				results[m.Name] = err.Error()
			} else {
				results[m.Name] = "ok"
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results, "lists": st.Status()})
	})
	r.With(s.requireUnlocked).Delete("/api/adblock/lists/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.adblockStore().Delete(chi.URLParam(r, "name")); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
}

// adblockStore re-applies the current settings (directory, custom lists)
// before each use, the same way the categories route re-points its store.
func (s *Server) adblockStore() *adblock.Store {
	s.Adblock.Apply(s.Settings().Adblock)
	return s.Adblock
}

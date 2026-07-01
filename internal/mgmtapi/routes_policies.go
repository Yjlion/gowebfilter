package mgmtapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/models"
)

func (s *Server) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	list, err := s.Policies.List()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	p, err := s.Policies.Get(name)
	if errors.Is(err, config.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "Policy '"+name+"' not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleCreatePolicy(w http.ResponseWriter, r *http.Request) {
	p := models.NewPolicy()
	if err := readJSON(r, &p); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid policy body")
		return
	}
	if err := s.Policies.Create(p); err != nil {
		if errors.Is(err, config.ErrExists) {
			writeJSONError(w, http.StatusConflict, "Policy '"+p.Name+"' already exists")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	p := models.NewPolicy()
	if err := readJSON(r, &p); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid policy body")
		return
	}
	if err := s.Policies.Update(name, p); err != nil {
		switch {
		case errors.Is(err, config.ErrNotFound):
			writeJSONError(w, http.StatusNotFound, "Policy '"+name+"' not found")
		case errors.Is(err, config.ErrExists):
			writeJSONError(w, http.StatusConflict, "Policy '"+p.Name+"' already exists")
		default:
			writeJSONError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := s.Policies.Delete(name); err != nil {
		if errors.Is(err, config.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "Policy '"+name+"' not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleListTournaments(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	list, err := s.Store.ListTournaments(r.Context(), status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetTournament(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.GetTournament(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "турнир не найден")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

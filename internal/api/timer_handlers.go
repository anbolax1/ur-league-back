package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ur-league/backend/internal/models"
)

// Границы времени на задание, секунд: меньше - не успеть показать, больше - таймер забыт включённым.
const (
	minTimerSec = 5
	maxTimerSec = 60 * 60
)

// POST /api/tournaments/{id}/timer {seconds} - запустить таймер задания на время; запущенный начинается заново.
func (s *Server) handleStartTimer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !s.matchGuard(w, r, id) {
		return
	}
	var b struct {
		Seconds int `json:"seconds"`
	}
	if err := readJSON(r, &b); err != nil || b.Seconds < minTimerSec || b.Seconds > maxTimerSec {
		writeError(w, http.StatusBadRequest, "время таймера - от 5 секунд до часа")
		return
	}
	if !s.setTimer(w, r, id, &models.LiveTimer{DurationSec: b.Seconds, EndsAt: time.Now().Add(time.Duration(b.Seconds) * time.Second).UnixMilli()}) {
		return
	}
	s.writeMatch(w, r, id, http.StatusOK)
}

// DELETE /api/tournaments/{id}/timer - убрать таймер с экрана.
func (s *Server) handleStopTimer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !s.matchGuard(w, r, id) {
		return
	}
	if !s.setTimer(w, r, id, nil) {
		return
	}
	s.writeMatch(w, r, id, http.StatusOK)
}

// setTimer сохраняет таймер в состоянии оверлея; таймер есть только у идущего раунда.
func (s *Server) setTimer(w http.ResponseWriter, r *http.Request, tournamentID string, t *models.LiveTimer) bool {
	st, err := s.Store.GetMatchState(r.Context(), tournamentID)
	if err != nil {
		writeError(w, http.StatusNotFound, "матч не найден")
		return false
	}
	if st.Stage != "round" {
		writeError(w, http.StatusConflict, "раунд не идёт")
		return false
	}
	stored := s.storedLiveState(r.Context())
	stored.TournamentID = &tournamentID
	stored.Timer = t
	norm, err := json.Marshal(stored)
	if err == nil {
		err = s.Store.SetLiveState(r.Context(), norm)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	return true
}

package api

import (
	"errors"
	"net/http"

	"github.com/ur-league/backend/internal/store"
)

// GET /api/belt - пояс лиги: у кого он, сколько на нём ржавчины и вся история.
func (s *Server) handleBelt(w http.ResponseWriter, r *http.Request) {
	b, err := s.Store.Belt(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// POST /api/belt/transfer {toUserId} - организатор передаёт пояс другому игроку без матча.
func (s *Server) handleTransferBelt(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ToUserID string `json:"toUserId"`
	}
	if err := readJSON(r, &b); err != nil || b.ToUserID == "" {
		writeError(w, http.StatusBadRequest, "некорректный JSON")
		return
	}
	belt, err := s.Store.TransferBelt(r.Context(), actorID(r), b.ToUserID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "Игрок не найден")
		return
	case errors.Is(err, store.ErrBeltVacant), errors.Is(err, store.ErrBeltSame):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.SyncBeltNews(r.Context())
	writeJSON(w, http.StatusOK, belt)
}

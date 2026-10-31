package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/ur-league/backend/internal/media"
	"github.com/ur-league/backend/internal/models"
	"github.com/ur-league/backend/internal/store"
)

const (
	avatarDir      = "avatars"            // папка фото игроков в хранилище медиа
	maxAvatarBytes = 5 << 20              // фото профиля легче обложки матча
	minNickLen     = 2                    // ник на сайте: от 2 до 24 знаков
	maxNickLen     = 24                   // длиннее не помещается в строку рейтинга
	maxReasonLen   = 300                  // причина отказа видна игроку в профиле
	viewerCanApply = models.RoleModerator // с этой роли фото и ник меняются без проверки
)

// cleanNick приводит ник к одной строке и проверяет длину и символы.
func cleanNick(raw string) (string, string) {
	nick := strings.Join(strings.Fields(raw), " ")
	if n := utf8.RuneCountInString(nick); n < minNickLen || n > maxNickLen {
		return "", fmt.Sprintf("ник — от %d до %d знаков", minNickLen, maxNickLen)
	}
	for _, r := range nick {
		if unicode.IsControl(r) || r == '<' || r == '>' {
			return "", "в нике нельзя использовать служебные символы"
		}
	}
	return nick, ""
}

func writeChangeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "заявка не найдена")
	case errors.Is(err, store.ErrNameTaken):
		writeError(w, http.StatusConflict, "такой ник уже занят")
	case errors.Is(err, store.ErrChangeDecided):
		writeError(w, http.StatusConflict, "заявка уже рассмотрена")
	default:
		writeError(w, http.StatusInternalServerError, "не удалось сохранить")
	}
}

// POST /api/me/name {name} - новый ник на сайте: у игрока уходит на проверку, у организатора и модератора меняется сразу.
func (s *Server) handleChangeName(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON")
		return
	}
	nick, msg := cleanNick(body.Name)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if nick == u.DisplayName {
		writeError(w, http.StatusBadRequest, "это уже твой ник")
		return
	}
	if u.Role.AtLeast(viewerCanApply) {
		if _, err := s.Store.SetProfileDirect(r.Context(), u.ID, store.ChangeName, nick); err != nil {
			writeChangeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"applied": true})
		return
	}
	change, _, err := s.Store.SubmitProfileChange(r.Context(), u.ID, store.ChangeName, nick)
	if err != nil {
		writeChangeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, change)
}

// POST /api/me/avatar - фото профиля файлом в теле запроса: у игрока уходит на проверку, у организатора и модератора
// ставится сразу.
func (s *Server) handleChangeAvatar(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	// Новое имя у каждого фото: браузер не покажет прежнее из кэша.
	name := fmt.Sprintf("%s-%d", u.ID, time.Now().UnixNano())
	rel, err := s.Media.SaveImage(avatarDir, name, http.MaxBytesReader(w, r.Body, maxAvatarBytes+1))
	if err != nil {
		if errors.Is(err, media.ErrImageTooLarge) {
			writeError(w, http.StatusBadRequest, "фото больше 5 МБ")
			return
		}
		writePreviewError(w, err)
		return
	}
	if u.Role.AtLeast(viewerCanApply) {
		dropped, err := s.Store.SetProfileDirect(r.Context(), u.ID, store.ChangeAvatar, rel)
		if err != nil {
			s.Media.Remove(rel)
			writeChangeError(w, err)
			return
		}
		s.Media.Remove(dropped)
		writeJSON(w, http.StatusOK, map[string]any{"applied": true})
		return
	}
	change, replaced, err := s.Store.SubmitProfileChange(r.Context(), u.ID, store.ChangeAvatar, rel)
	if err != nil {
		s.Media.Remove(rel)
		writeChangeError(w, err)
		return
	}
	s.Media.Remove(replaced)
	writeJSON(w, http.StatusCreated, change)
}

// DELETE /api/me/avatar - убрать своё фото; проверка не нужна.
func (s *Server) handleClearAvatar(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	dropped, err := s.Store.ClearAvatar(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось убрать фото")
		return
	}
	s.Media.Remove(dropped)
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/me/profile-changes - свои последние заявки с ответом проверяющего.
func (s *Server) handleMyProfileChanges(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	list, err := s.Store.MyProfileChanges(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// DELETE /api/me/profile-changes/{id} - забрать свою нерассмотренную заявку.
func (s *Server) handleCancelProfileChange(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	dropped, err := s.Store.CancelProfileChange(r.Context(), chi.URLParam(r, "id"), u.ID)
	if err != nil {
		writeChangeError(w, err)
		return
	}
	s.Media.Remove(dropped)
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/profile-changes - заявки на проверку (модератор и организатор).
func (s *Server) handlePendingProfileChanges(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.PendingProfileChanges(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// POST /api/profile-changes/{id}/decide {approve, reason} - одобрить заявку или отклонить с причиной.
func (s *Server) handleDecideProfileChange(w http.ResponseWriter, r *http.Request) {
	me, _ := userFrom(r.Context())
	var body struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if utf8.RuneCountInString(reason) > maxReasonLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("причина — до %d знаков", maxReasonLen))
		return
	}
	change, dropped, err := s.Store.DecideProfileChange(r.Context(), chi.URLParam(r, "id"), body.Approve, reason, me.ID)
	if err != nil {
		writeChangeError(w, err)
		return
	}
	s.Media.Remove(dropped)
	writeJSON(w, http.StatusOK, change)
}

package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/ur-league/backend/internal/store"
)

// Пределы анкеты заявки: рост чисел отсекает опечатки вроде лишнего нуля.
const (
	maxRegRank     = 40
	maxRegPlayTime = 300
	maxRegNote     = 500
	maxRegKd       = 99.99
	maxRegCount    = 10_000_000
)

// handleRegister - подача заявки в общий пул с анкетой: ID и ник в игре, ранг, статистика, удобное время.
// ID и ник заодно попадают в профиль, чтобы у игрока везде были одни и те же.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	var body struct {
		GameID   *string  `json:"gameId"`
		GameNick *string  `json:"gameNick"`
		Rank     string   `json:"rank"`
		Kd       *float64 `json:"kd"`
		Matches  *int     `json:"matches"`
		Wins     *int     `json:"wins"`
		Kills    *int     `json:"kills"`
		PlayTime string   `json:"playTime"`
		Note     string   `json:"note"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON")
		return
	}
	// Не присланные ID и ник берутся из профиля: так заявка работает и без новой анкеты.
	gameID, gameNick := u.GameID, u.GameNick
	if body.GameID != nil {
		gameID = strings.Join(strings.Fields(*body.GameID), "")
	}
	if body.GameNick != nil {
		gameNick = strings.Join(strings.Fields(*body.GameNick), " ")
	}
	form := store.RegistrationForm{
		GameID: gameID, GameNick: gameNick,
		Rank:     strings.Join(strings.Fields(body.Rank), " "),
		PlayTime: strings.TrimSpace(body.PlayTime),
		Note:     strings.TrimSpace(body.Note),
		Kd:       body.Kd, Matches: body.Matches, Wins: body.Wins, Kills: body.Kills,
	}
	if msg := validRegistrationForm(form); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if form.GameID != u.GameID || form.GameNick != u.GameNick {
		if _, err := s.Store.UpdateGameProfile(r.Context(), u.ID, form.GameID, form.GameNick); errors.Is(err, store.ErrGameIDTaken) {
			writeError(w, http.StatusConflict, "Этот игровой ID уже указан в профиле другого игрока")
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, "не удалось сохранить игровой ID")
			return
		}
	}
	reg, err := s.Store.CreateRegistration(r.Context(), u.ID, form)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, reg)
}

// validRegistrationForm проверяет анкету; пустая строка - всё в порядке.
func validRegistrationForm(f store.RegistrationForm) string {
	switch {
	case f.GameID != "" && !gameIDRe.MatchString(f.GameID):
		return "Игровой ID — только цифры, от 6 до 12"
	case utf8.RuneCountInString(f.GameNick) > maxGameNick:
		return fmt.Sprintf("Ник в игре — до %d символов", maxGameNick)
	case utf8.RuneCountInString(f.Rank) > maxRegRank:
		return fmt.Sprintf("Ранг — до %d символов", maxRegRank)
	case utf8.RuneCountInString(f.PlayTime) > maxRegPlayTime:
		return fmt.Sprintf("Удобное время — до %d символов", maxRegPlayTime)
	case utf8.RuneCountInString(f.Note) > maxRegNote:
		return fmt.Sprintf("Пожелания — до %d символов", maxRegNote)
	case f.Kd != nil && (*f.Kd < 0 || *f.Kd > maxRegKd || math.IsNaN(*f.Kd)):
		return "K/D — число от 0 до 99,99"
	}
	for _, n := range []*int{f.Matches, f.Wins, f.Kills} {
		if n != nil && (*n < 0 || *n > maxRegCount) {
			return "Матчи, победы и убийства — целые числа от 0"
		}
	}
	if f.Matches != nil && f.Wins != nil && *f.Wins > *f.Matches {
		return "Побед не может быть больше, чем матчей"
	}
	return ""
}

// handleListPool — открытые заявки (пул), кто подал раньше — выше. Organizer-only.
func (s *Server) handleListPool(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListPool(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleListPoolPage — пул заявок постранично: ?limit&offset → {items, total}. Organizer-only.
func (s *Server) handleListPoolPage(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, total, err := s.Store.ListPoolPage(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleMyRegistrations(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	list, err := s.Store.ListMyRegistrations(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleDecideRegistration — отклонить заявку из пула (или вернуть). Постановка
// игрока в турнир (accepted) выполняется автоматически при добавлении участника.
func (s *Server) handleDecideRegistration(w http.ResponseWriter, r *http.Request) {
	regID := chi.URLParam(r, "id")
	var body struct {
		Status string `json:"status"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON")
		return
	}
	if body.Status != "declined" && body.Status != "pending" {
		writeError(w, http.StatusBadRequest, "status должен быть declined или pending")
		return
	}
	reg, err := s.Store.DecideRegistration(r.Context(), regID, body.Status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, reg)
}

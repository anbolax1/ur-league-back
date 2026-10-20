package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ur-league/backend/internal/models"
	"github.com/ur-league/backend/internal/store"
)

// GET /api/users/overview?limit&offset&q&sort — пользователи с агрегатами участия,
// постранично. Organizer-only. В отличие от публичного профиля, отдаёт email и статистику участия.
func (s *Server) handleListUsersOverview(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, total, err := s.Store.ListUsersOverview(r.Context(), limit, offset,
		r.URL.Query().Get("q"), r.URL.Query().Get("sort"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ids := make([]string, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	if tags, err := s.Store.TagsForUsers(r.Context(), ids, false); err == nil {
		for i := range items {
			items[i].Tags = tags[items[i].ID]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// PATCH /api/users/{id}/role — назначить роль пользователю. Superadmin-only (см. server.go).
// Инвариант: нельзя снять роль у последнего организатора — иначе кабинет станет недоступен никому.
// Роль читается из БД на каждом запросе (injectUser), поэтому изменение действует сразу.
func (s *Server) handleSetUserRole(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Role string `json:"role"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON")
		return
	}
	role := models.Role(strings.TrimSpace(body.Role))
	if !role.Valid() {
		writeError(w, http.StatusBadRequest, "неизвестная роль")
		return
	}

	target, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "пользователь не найден")
		return
	}

	// Понижение единственного организатора запрещено.
	if target.Role == models.RoleSuperadmin && role != models.RoleSuperadmin {
		n, err := s.Store.CountSuperadmins(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "не удалось проверить роли")
			return
		}
		if n <= 1 {
			writeError(w, http.StatusConflict, "нельзя снять роль у последнего организатора")
			return
		}
	}

	if err := s.Store.SetRole(r.Context(), id, role); err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось изменить роль")
		return
	}
	updated, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось загрузить пользователя")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// GET /api/players/{login} — публичный профиль игрока: user + сезон-статы + история (B6).
func (s *Server) handleGetPlayer(w http.ResponseWriter, r *http.Request) {
	login := chi.URLParam(r, "login")
	u, err := s.Store.GetUserByLogin(r.Context(), login)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "игрок не найден")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	u.Tags, _ = s.Store.UserTags(r.Context(), u.ID, true)
	// Целиком игровой ID видят сам игрок и организатор: по ID игрока находят в игре.
	if viewer, ok := userFrom(r.Context()); !ok || (viewer.ID != u.ID && !viewer.Role.AtLeast(models.RoleSuperadmin)) {
		u.GameID = maskGameID(u.GameID)
	}
	history, err := s.Store.PlayerHistory(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Источники очков и любимая карта — из стора (по participant-id завершённых турниров).
	stats, err := s.Store.PlayerAggregate(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Сводка и винрейт по режимам — из истории (только завершённые турниры).
	var points, wins, tournaments int
	for _, h := range history {
		if h.Status != "finished" {
			continue
		}
		points += h.Points
		tournaments++
		if h.Win {
			wins++
		}
		switch h.Mode {
		case "2x2":
			stats.DuoPlayed++
			if h.Win {
				stats.DuoWins++
			}
		case "3x3":
			stats.TrioPlayed++
			if h.Win {
				stats.TrioWins++
			}
		default:
			stats.SoloPlayed++
			if h.Win {
				stats.SoloWins++
			}
		}
	}
	// Текущая серия — ведущий ран одинаковых исходов (история отсортирована по дате убыв.).
	for _, h := range history {
		if h.Status != "finished" {
			continue
		}
		kind := "loss"
		if h.Win {
			kind = "win"
		}
		if stats.StreakKind == "" {
			stats.StreakKind = kind
			stats.StreakLen = 1
		} else if kind == stats.StreakKind {
			stats.StreakLen++
		} else {
			break
		}
	}
	mmrSolo, _ := s.Store.GetUserMmr(r.Context(), u.ID, "1x1")
	// У составов рейтинг у команд; для профиля - лучший MMR среди команд игрока в каждом формате.
	mmrDuo, _ := s.Store.BestTeamMmr(r.Context(), u.ID, "2x2")
	mmrTrio, _ := s.Store.BestTeamMmr(r.Context(), u.ID, "3x3")
	// Расширенная статистика 1×1 (по истории MMR) + аналитика + команды игрока.
	mmr1x1, timeline, analytics, _ := s.Store.PlayerStatsBundle(r.Context(), u.ID)
	teams, _ := s.Store.TeamsForUser(r.Context(), u.ID)
	patches, err := s.Store.PlayerPatches(r.Context(), u.ID)
	if err != nil {
		patches = map[string]models.PatchSeason{}
	}
	writeJSON(w, http.StatusOK, models.PlayerProfile{
		User:        u,
		MmrSolo:     mmrSolo,
		MmrDuo:      mmrDuo,
		MmrTrio:     mmrTrio,
		Points:      points,
		Wins:        wins,
		Tournaments: tournaments,
		Stats:       stats,
		History:     history,
		Mmr1x1:      mmr1x1,
		Timeline:    timeline,
		Analytics:   analytics,
		Teams:       teams,
		Patches:     patches,
	})
}

// maskGameID оставляет от игрового ID две первые и две последние цифры - этого хватает, чтобы
// узнать свой ID, но не чтобы найти игрока в игре.
func maskGameID(id string) string {
	if len(id) <= 4 {
		return strings.Repeat("•", len(id))
	}
	return id[:2] + strings.Repeat("•", len(id)-4) + id[len(id)-2:]
}

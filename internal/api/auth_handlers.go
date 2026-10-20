package api

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ur-league/backend/internal/auth"
	"github.com/ur-league/backend/internal/models"
	"github.com/ur-league/backend/internal/store"
)

// loginRe — допустимый логин: латиница, цифры и подчёркивание, 3–32 символа.
// Узкий набор исключает пробелы, омоглифы и спецсимволы (меньше путаницы и сюрпризов).
var loginRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,32}$`)

// gameIDRe — игровой ID PUBG Mobile: только цифры, от 6 до 12.
var gameIDRe = regexp.MustCompile(`^\d{6,12}$`)

// maxGameNick - ник в игре стоит в оверлее рядом с ником на сайте, поэтому короткий.
const maxGameNick = 32

const minPasswordLen = 8

type credentials struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

// clientIP возвращает IP клиента — ключ троттлинга. НЕ доверяем произвольным заголовкам
// (True-Client-IP, клиентский X-Forwarded-For легко подделать). За доверенным прокси
// (TRUST_PROXY=true) берём X-Real-IP, который nginx ставит из $remote_addr; иначе — реальный
// адрес TCP-соединения r.RemoteAddr.
func (s *Server) clientIP(r *http.Request) string {
	if s.Cfg.TrustProxy {
		if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
			return xr
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// validateCredentials проверяет формат логина/пароля. Возвращает нормализованный
// логин и человекочитаемую ошибку (пустая строка — всё ок).
func validateCredentials(c credentials) (login string, msg string) {
	login = strings.TrimSpace(c.Login)
	if !loginRe.MatchString(login) {
		return "", "логин: 3–32 символа, латиница, цифры и подчёркивание"
	}
	if len(c.Password) < minPasswordLen {
		return "", "пароль должен быть не короче 8 символов"
	}
	if len(c.Password) > auth.MaxPasswordBytes {
		return "", "пароль слишком длинный (не более 72 байт)"
	}
	if strings.EqualFold(strings.TrimSpace(c.Password), login) {
		return "", "пароль не должен совпадать с логином"
	}
	return login, ""
}

// handleSignup — регистрация по логину/паролю. Создаёт пользователя, сразу заводит
// сессию (httpOnly-cookie) и возвращает профиль. Логин в ORGANIZER_LOGINS получает
// роль organizer. Троттлинг по IP против массового создания аккаунтов.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.registerLimiter.blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "слишком много регистраций, попробуйте позже")
		return
	}

	var body credentials
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON")
		return
	}
	login, msg := validateCredentials(body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось обработать пароль")
		return
	}

	// Открытая регистрация всегда создаёт обычного пользователя. Роль superadmin выдаётся
	// только бутстрапом организатора при старте и затем — другим организатором вручную
	// (PATCH /users/{id}/role). Никакого самоназначения роли по совпадению логина.
	role := models.DefaultRole

	// Каждая попытка (даже неудачная) считается — иначе лимит легко обойти перебором занятых логинов.
	s.registerLimiter.inc(ip)

	u, err := s.Store.CreateUser(r.Context(), login, login, hash, role)
	if errors.Is(err, store.ErrLoginTaken) {
		writeError(w, http.StatusConflict, "логин уже занят")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось создать пользователя")
		return
	}

	token, err := auth.IssueToken(s.Cfg.JWTSecret, u.ID, string(u.Role), sessionTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось создать сессию")
		return
	}
	s.setSessionCookie(w, r, token)
	writeJSON(w, http.StatusCreated, u)
}

// handleLogin — вход по логину/паролю. На неверные данные отвечает одинаково
// («неверный логин или пароль») и тратит время bcrypt даже при отсутствии пользователя,
// чтобы не раскрывать существование логина. Троттлинг по IP и по логину против перебора.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.loginIPLimiter.blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "слишком много попыток входа, попробуйте позже")
		return
	}
	s.loginIPLimiter.inc(ip)

	var body credentials
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON")
		return
	}
	login := strings.TrimSpace(body.Login)
	if login == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "укажите логин и пароль")
		return
	}

	loginKey := strings.ToLower(login)
	if s.loginUserLimiter.blocked(loginKey) {
		writeError(w, http.StatusTooManyRequests, "слишком много попыток входа, попробуйте позже")
		return
	}

	id, role, hash, err := s.Store.GetUserAuthByLogin(r.Context(), login)
	if errors.Is(err, store.ErrNotFound) {
		// Сравниваем с фиктивным хешем, чтобы время ответа не выдавало отсутствие логина.
		auth.CheckPassword(s.dummyHash, body.Password)
		s.loginUserLimiter.inc(loginKey)
		writeError(w, http.StatusUnauthorized, "неверный логин или пароль")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось выполнить вход")
		return
	}
	if !auth.CheckPassword(hash, body.Password) {
		s.loginUserLimiter.inc(loginKey)
		writeError(w, http.StatusUnauthorized, "неверный логин или пароль")
		return
	}

	// Успех — сбрасываем счётчик неудач по логину.
	s.loginUserLimiter.reset(loginKey)

	token, err := auth.IssueToken(s.Cfg.JWTSecret, id, string(role), sessionTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось создать сессию")
		return
	}
	s.setSessionCookie(w, r, token)

	u, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось загрузить профиль")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	u.Tags, _ = s.Store.UserTags(r.Context(), u.ID, true)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Сдвигаем эпоху сессий — текущий (и любой ранее выпущенный) токен становится недействителен
	// на сервере, а не только удаляется cookie в браузере.
	if u, ok := userFrom(r.Context()); ok {
		_ = s.Store.RevokeSessions(r.Context(), u.ID)
	}
	s.clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// PATCH /api/me {gameId?, gameNick?} - игровой ID и ник в игре; не присланное поле не меняется,
// пустое - стирается.
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	var body struct {
		GameID   *string `json:"gameId"`
		GameNick *string `json:"gameNick"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный JSON")
		return
	}
	gameID, gameNick := u.GameID, u.GameNick
	if body.GameID != nil {
		// ID переписывают с экрана профиля в игре, где цифры бывают разбиты пробелами.
		gameID = strings.Join(strings.Fields(*body.GameID), "")
	}
	if body.GameNick != nil {
		gameNick = strings.Join(strings.Fields(*body.GameNick), " ")
	}
	if gameID != "" && !gameIDRe.MatchString(gameID) {
		writeError(w, http.StatusBadRequest, "Игровой ID — только цифры, от 6 до 12")
		return
	}
	if utf8.RuneCountInString(gameNick) > maxGameNick {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Ник в игре — до %d символов", maxGameNick))
		return
	}
	updated, err := s.Store.UpdateGameProfile(r.Context(), u.ID, gameID, gameNick)
	if errors.Is(err, store.ErrGameIDTaken) {
		writeError(w, http.StatusConflict, "Этот игровой ID уже указан в профиле другого игрока")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось обновить профиль")
		return
	}
	// Держим пул актуальным: новый ID и ник сразу видны в незакрытой заявке игрока.
	_ = s.Store.SyncPendingRegistrationGame(r.Context(), u.ID, updated.GameID, updated.GameNick)
	writeJSON(w, http.StatusOK, updated)
}

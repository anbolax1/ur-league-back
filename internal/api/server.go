package api

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ur-league/backend/internal/auth"
	"github.com/ur-league/backend/internal/config"
	"github.com/ur-league/backend/internal/media"
	"github.com/ur-league/backend/internal/models"
	"github.com/ur-league/backend/internal/store"
	"github.com/ur-league/backend/internal/ws"
)

type Server struct {
	Cfg   config.Config
	Store *store.Store
	Hub   *ws.Hub
	Media *media.Processor

	// presetsRev — счётчик правок пресетов оверлея. Едет в конверте состояния по WS;
	// оверлеи, привязанные к пресету ссылкой, по его смене перечитывают раскладку.
	// В памяти (перезапуск обнуляет) — клиенты увидят «другое» значение и перечитают
	// пресет лишний раз, что безвредно.
	presetsRev atomic.Int64

	// dummyHash — фиктивный bcrypt-хеш для выравнивания времени ответа на вход
	// несуществующего логина (защита от перебора пользователей по таймингу).
	dummyHash string
	// Троттлинг входа/регистрации (защита от перебора паролей и спама аккаунтов).
	loginIPLimiter   *limiter
	loginUserLimiter *limiter
	registerLimiter  *limiter
}

func New(cfg config.Config, st *store.Store, hub *ws.Hub) *Server {
	// Хеш считаем один раз при старте — его значение неважно, важна постоянная стоимость сравнения.
	dummy, _ := auth.HashPassword("placeholder-not-a-real-account-password")
	return &Server{
		Cfg:              cfg,
		Store:            st,
		Hub:              hub,
		Media:            media.NewProcessor(cfg.MediaDir, cfg.YtDlpPath, cfg.FfmpegPath, cfg.FfprobePath),
		dummyHash:        dummy,
		loginIPLimiter:   newLimiter(15, 5*time.Minute), // грубый предохранитель против долбёжки с одного IP
		loginUserLimiter: newLimiter(8, 15*time.Minute), // против перебора пароля к конкретному логину
		registerLimiter:  newLimiter(6, time.Hour),      // против массовой регистрации с одного IP
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(s.cors)

	r.Route("/api", func(r chi.Router) {
		r.Use(s.injectUser)

		r.Get("/health", s.handleHealth)

		// --- Auth (логин/пароль) ---
		r.Post("/auth/register", s.handleSignup)
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/logout", s.handleLogout)

		// --- Public reads ---
		r.Get("/tournaments", s.handleListTournaments)
		r.Get("/tournaments/{id}", s.handleGetTournament)
		r.Get("/leaderboard", s.handleLeaderboard)
		r.Get("/seasons", s.handleListSeasons)
		r.Get("/seasons/{key}/recap", s.handleSeasonRecap)
		r.Get("/players/{login}", s.handleGetPlayer)
		r.Get("/patches", s.handlePatchCatalog)
		r.Get("/belt", s.handleBelt)
		r.Get("/teams/{teamKey}", s.handleGetTeam)
		r.Get("/claim/{token}", s.handleClaimInfo)
		r.Post("/claim/{token}", s.handleClaim)
		r.Get("/rules", s.handleRules)
		r.Get("/legendary", s.handleListLegendary)
		r.Get("/maps", s.handleListMaps)
		r.Get("/matches/current", s.handleCurrentMatch)
		r.Get("/tournaments/{id}/match", s.handleGetMatch)
		r.Get("/tournaments/{id}/matchup", s.handleMatchup)
		r.Get("/overlay/state", s.handleGetOverlayState)
		r.Get("/overlay/layout", s.handleGetOverlayLayout)
		r.Get("/overlay/preset/{key}", s.handleGetOverlayPreset) // раскладка для ссылки /overlay/<slug> в OBS
		r.Get("/ws/overlay", s.handleOverlayWS)
		r.Get("/highlights", s.handleListHighlights)
		r.Get("/media/*", s.handleServeMedia)
		r.Get("/news", s.handleListNews)
		r.Get("/news/{id}", s.handleGetNews)

		// --- Authenticated user ---
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/auth/me", s.handleMe)
			r.Patch("/me", s.handleUpdateMe)
			r.Get("/me/tags", s.handleMyTags)
			r.Put("/me/tags/{id}", s.handleSetMyTag)
			r.Get("/me/registrations", s.handleMyRegistrations)
			r.Post("/registrations", s.handleRegister)
			r.Post("/highlights", s.handleCreateHighlight)
		})

		// --- Superadmin only (организатор) ---
		r.Group(func(r chi.Router) {
			r.Use(s.requireRole(models.RoleSuperadmin))
			// Матч: создание, раунды с заходами, зачёт, ручные очки, журнал, завершение.
			r.Get("/match-players", s.handleListMatchPlayers)
			r.Post("/players/placeholder", s.handleCreatePlaceholder)
			r.Post("/matches", s.handleCreateMatch)
			r.Post("/tournaments/{id}/first", s.handleFirstSide)
			r.Post("/tournaments/{id}/rounds/next", s.handleNextRound)
			r.Post("/tournaments/{id}/start", s.handleStartMatch)
			r.Get("/tags", s.handleListTags)
			r.Post("/tags", s.handleCreateTag)
			r.Patch("/tags/{id}", s.handleUpdateTag)
			r.Delete("/tags/{id}", s.handleDeleteTag)
			r.Post("/tags/{id}/holders", s.handleAddTagHolder)
			r.Delete("/tags/{id}/holders/{userId}", s.handleRemoveTagHolder)
			r.Post("/tournaments/{id}/schedule", s.handleRescheduleMatch)
			r.Post("/tournaments/{id}/prize", s.handleSetPrize)
			r.Put("/tournaments/{id}/preview", s.handleSetPreview)
			r.Delete("/tournaments/{id}/preview", s.handleDeletePreview)
			r.Post("/tournaments/{id}/finish", s.handleFinishMatch)
			r.Post("/tournaments/{id}/cancel", s.handleCancelMatch)
			r.Post("/tournaments/{id}/focus", s.handleMatchFocus)
			r.Post("/tournaments/{id}/heat/map", s.handleHeatMap)
			r.Post("/tournaments/{id}/heat/place", s.handleHeatPlace)
			r.Post("/tournaments/{id}/points", s.handleMatchPoints)
			r.Post("/tournaments/{id}/knocks", s.handleMatchKnock)
			r.Post("/tournaments/{id}/legendary", s.handleMatchLegendary)
			r.Post("/tournaments/{id}/undo", s.handleMatchUndo)
			r.Post("/round-bonus-tasks/{id}/mark", s.handleMarkTask)
			r.Post("/round-bonus-tasks/{id}/reroll", s.handleRerollTask)

			r.Get("/users/overview", s.handleListUsersOverview)
			r.Patch("/users/{id}/role", s.handleSetUserRole)
			r.Post("/users/{id}/claim-link", s.handleCreateClaimLink)
			r.Get("/registrations/pool", s.handleListPool)
			r.Get("/registrations/pool/page", s.handleListPoolPage)
			r.Post("/registrations/{id}/decide", s.handleDecideRegistration)

			// Сезоны рейтинга: начать новый (завершает текущий активный); удалить (турниры отвязываются).
			r.Post("/seasons", s.handleStartSeason)
			r.Patch("/seasons/{id}", s.handleUpdateSeason)
			r.Delete("/seasons/{id}", s.handleDeleteSeason)

			// Новости лиги: черновики, публикация по дате, картинка.
			r.Get("/news/all", s.handleListAllNews)
			r.Post("/news", s.handleCreateNews)
			r.Patch("/news/{id}", s.handleUpdateNews)
			r.Delete("/news/{id}", s.handleDeleteNews)
			r.Put("/news/{id}/image", s.handleSetNewsImage)
			r.Delete("/news/{id}/image", s.handleDeleteNewsImage)

			// Сцены оверлея (в коде - пресеты): у каждой своя ссылка для OBS, выбранную показывает /overlay.
			r.Put("/overlay/active", s.handleSetActiveOverlayScene)
			r.Get("/overlay/presets", s.handleListOverlayPresets)
			r.Post("/overlay/presets", s.handleCreateOverlayPreset)
			r.Put("/overlay/presets/{id}", s.handleUpdateOverlayPreset)
			r.Delete("/overlay/presets/{id}", s.handleDeleteOverlayPreset)

			// Справочник заданий (редактирование организатором)
			r.Post("/catalog/tasks", s.handleCreateTask)
			r.Post("/catalog/tasks/bulk", s.handleBulkCreateTasks)
			r.Patch("/catalog/tasks/{id}", s.handleUpdateTask)
			r.Delete("/catalog/tasks/{id}", s.handleDeleteTask)

			// Легендарные контракты: каталог + отметка выполнения (навсегда) / возврат в пул.
			r.Post("/catalog/legendary", s.handleCreateLegendary)
			r.Patch("/catalog/legendary/{id}", s.handleUpdateLegendary)
			r.Delete("/catalog/legendary/{id}", s.handleDeleteLegendary)
			r.Post("/legendary/{id}/complete", s.handleCompleteLegendary)
			r.Post("/legendary/{id}/reopen", s.handleUncompleteLegendary)

			// Хайлайты: модерация (очередь, одобрить/отклонить, удалить).
			r.Get("/highlights/moderation", s.handleListHighlightsModeration)
			r.Post("/highlights/{id}/moderate", s.handleModerateHighlight)
			r.Delete("/highlights/{id}", s.handleDeleteHighlight)
		})
	})

	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"overlayConns": s.Hub.Count(),
	})
}

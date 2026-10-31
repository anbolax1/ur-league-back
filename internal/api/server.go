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
	queueLimiter     *limiter
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
		loginIPLimiter:   newLimiter(15, 5*time.Minute),           // грубый предохранитель против долбёжки с одного IP
		loginUserLimiter: newLimiter(8, 15*time.Minute),           // против перебора пароля к конкретному логину
		registerLimiter:  newLimiter(6, time.Hour),                // против массовой регистрации с одного IP
		queueLimiter:     newLimiter(queueSelfLimit, time.Minute), // запись, выход и подтверждение одного игрока
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
		r.Get("/hidden-players", s.handleHiddenPlayers)
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
		r.Get("/tournaments/{id}/chance", s.handleMatchChance)
		r.Get("/overlay/state", s.handleGetOverlayState)
		r.Get("/overlay/layout", s.handleGetOverlayLayout)
		r.Get("/overlay/preset/{key}", s.handleGetOverlayPreset) // раскладка для ссылки /overlay/<slug> в OBS
		r.Get("/ws/overlay", s.handleOverlayWS)
		r.Get("/highlights", s.handleListHighlights)
		r.Get("/media/*", s.handleServeMedia)
		r.Get("/news", s.handleListNews)
		r.Get("/news/{id}", s.handleGetNews)

		// --- Очередь выступлений: чтение открыто всем ---
		r.Get("/queue", s.handleQueue)
		r.Get("/queue/sessions", s.handleListQueueSessions)
		r.Get("/queue/sessions/{id}", s.handleQueueSession)

		// --- Authenticated user ---
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/auth/me", s.handleMe)
			r.Patch("/me", s.handleUpdateMe)
			r.Get("/me/tags", s.handleMyTags)
			r.Put("/me/tags/{id}", s.handleSetMyTag)
			r.Get("/me/registrations", s.handleMyRegistrations)
			r.Post("/me/name", s.handleChangeName)
			r.Post("/me/avatar", s.handleChangeAvatar)
			r.Delete("/me/avatar", s.handleClearAvatar)
			r.Get("/me/profile-changes", s.handleMyProfileChanges)
			r.Delete("/me/profile-changes/{id}", s.handleCancelProfileChange)
			// Очередь выступлений: запись, выход и подтверждение готовности - только за себя.
			r.Get("/queue/me", s.handleQueueMyCalls)
			r.Post("/queue/sessions/{id}/join", s.handleQueueJoin)
			r.Post("/queue/sessions/{id}/leave", s.handleQueueLeave)
			r.Post("/queue/sessions/{id}/confirm", s.handleQueueConfirm)
			r.Get("/me/tokens", s.handleMyTokens)
			r.Post("/me/reset", s.handleResetRating)
			r.Post("/registrations", s.handleRegister)
			r.Post("/highlights", s.handleCreateHighlight)
		})

		// --- Модератор и организатор: проверка фото и ников игроков ---
		r.Group(func(r chi.Router) {
			r.Use(s.requireRole(models.RoleModerator))
			r.Get("/profile-changes", s.handlePendingProfileChanges)
			r.Post("/profile-changes/{id}/decide", s.handleDecideProfileChange)
			// Жетоны игроков: выдача, «Без очереди», ×2 на матч.
			r.Get("/users/{id}/moderation", s.handlePlayerModeration)
			r.Post("/users/{id}/tokens", s.handleGrantTokens)
			r.Post("/users/{id}/tokens/priority", s.handleActivatePriority)
			r.Delete("/users/{id}/tokens/priority", s.handleCancelPriority)
			r.Post("/tournaments/{id}/double", s.handleMatchDouble)

			// Очередь выступлений (организатор и модератор): сессии, вызовы, порядок, матчи, журнал.
			r.Post("/queue/sessions", s.handleCreateQueueSession)
			r.Patch("/queue/sessions/{id}", s.handleUpdateQueueSession)
			r.Post("/queue/sessions/{id}/mode", s.handleQueueMode)
			r.Post("/queue/sessions/{id}/finish", s.handleFinishQueueSession)
			r.Post("/queue/sessions/{id}/call", s.handleQueueCall)
			r.Post("/queue/sessions/{id}/entries", s.handleQueueAdd)
			r.Post("/queue/sessions/{id}/entries/{entryId}/confirm", s.handleQueueConfirmEntry)
			r.Post("/queue/sessions/{id}/entries/{entryId}/skip", s.handleQueueSkip)
			r.Post("/queue/sessions/{id}/entries/{entryId}/return", s.handleQueueReturn)
			r.Post("/queue/sessions/{id}/entries/{entryId}/move", s.handleQueueMove)
			r.Delete("/queue/sessions/{id}/entries/{entryId}", s.handleQueueRemove)
			r.Post("/queue/sessions/{id}/match/start", s.handleQueueMatchStart)
			r.Post("/queue/sessions/{id}/match/finish", s.handleQueueMatchFinish)
			r.Post("/queue/sessions/{id}/match/cancel", s.handleQueueMatchCancel)
			r.Get("/queue/sessions/{id}/events", s.handleQueueEvents)
			// Список игроков нужен модератору, чтобы добавлять в очередь вручную.
			r.Get("/match-players", s.handleListMatchPlayers)
		})

		// --- Superadmin only (организатор) ---
		r.Group(func(r chi.Router) {
			r.Use(s.requireRole(models.RoleSuperadmin))
			// Матч: создание, раунды с заходами, зачёт, ручные очки, журнал, завершение.
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
			r.Post("/tournaments/{id}/duel", s.handleMatchDuel)
			r.Post("/round-bonus-tasks/{id}/mark", s.handleMarkTask)
			r.Post("/round-bonus-tasks/{id}/reroll", s.handleRerollTask)

			r.Get("/users/overview", s.handleListUsersOverview)
			r.Patch("/users/{id}/role", s.handleSetUserRole)
			r.Patch("/users/{id}/hidden", s.handleSetUserHidden)
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

package store_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ur-league/backend/internal/db"
	"github.com/ur-league/backend/internal/models"
	"github.com/ur-league/backend/internal/store"
)

// Место захода пишется, отменяется из журнала и идёт в статистику игрока: лучшее, среднее, чикены.
// Запуск - как у TestMMRIntegration.
func TestHeatPlaceIntegration(t *testing.T) {
	url := os.Getenv("UR_TEST_DB")
	if url == "" {
		t.Skip("UR_TEST_DB не задан")
	}
	if err := db.Migrate(url); err != nil {
		t.Fatalf("миграции: %v", err)
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatalf("подключение: %v", err)
	}
	defer pool.Close()
	st := store.New(pool)

	login := fmt.Sprintf("hp_%d", time.Now().UnixNano())
	user, err := st.CreateUser(ctx, login, login, "x", models.RoleUser)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	tour, err := st.CreateTournament(ctx, models.Tournament{Title: "hp-places", Mode: "1x1"})
	if err != nil {
		t.Fatalf("CreateTournament: %v", err)
	}
	defer func() { _ = st.DeleteTournament(ctx, tour.ID) }()
	side, err := st.AddParticipant(ctx, models.Participant{TournamentID: tour.ID, Name: login, Seed: 1, UserID: &user.ID})
	if err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}
	rounds, err := st.ListRounds(ctx, tour.ID)
	if err != nil || len(rounds) == 0 {
		t.Fatalf("раунды: %v", err)
	}
	r2, err := st.CreateRound(ctx, models.Round{TournamentID: tour.ID, Number: 2})
	if err != nil {
		t.Fatalf("CreateRound: %v", err)
	}
	place := func(n int) *int { return &n }

	if _, err := st.SetHeatPlace(ctx, rounds[0].ID, side.ID, place(1)); err != nil {
		t.Fatalf("место 1: %v", err)
	}
	prev, err := st.SetHeatPlace(ctx, r2.ID, side.ID, place(6))
	if err != nil || prev != nil {
		t.Fatalf("место 6: прежнее %v, %v", prev, err)
	}
	prev, err = st.SetHeatPlace(ctx, r2.ID, side.ID, place(4))
	if err != nil || prev == nil || *prev != 6 {
		t.Fatalf("исправление на 4: прежнее %v, %v", prev, err)
	}
	if err := st.AddMatchLog(ctx, tour.ID, 2, &side.ID, "place", "место", 0, map[string]any{
		"type": "place", "roundId": r2.ID, "participantId": side.ID, "prevPlace": prev,
	}); err != nil {
		t.Fatalf("AddMatchLog: %v", err)
	}
	if _, err := st.UndoLastMatchLog(ctx, tour.ID); err != nil {
		t.Fatalf("UndoLastMatchLog: %v", err)
	}
	ms, err := st.GetMatchState(ctx, tour.ID)
	if err != nil {
		t.Fatalf("GetMatchState: %v", err)
	}
	got := map[int]int{}
	for _, h := range ms.Heats {
		if h.Place != nil {
			got[h.RoundNumber] = *h.Place
		}
	}
	if got[1] != 1 || got[2] != 6 {
		t.Fatalf("места заходов после отмены: %v, want раунд 1 - 1, раунд 2 - 6", got)
	}

	if _, err := st.UpdateTournamentStatus(ctx, tour.ID, "finished"); err != nil {
		t.Fatalf("завершение: %v", err)
	}
	stats, err := st.PlayerAggregate(ctx, user.ID)
	if err != nil {
		t.Fatalf("PlayerAggregate: %v", err)
	}
	if stats.PlacedHeats != 2 || stats.BestPlace != 1 || stats.AvgPlace != 3.5 || stats.Chickens != 1 {
		t.Errorf("места в статистике: %+v", stats)
	}
}

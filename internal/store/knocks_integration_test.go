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

// Киллы в пульте и статистика киллов игрока против живой Postgres. Запуск - как у TestMMRIntegration.
func TestKnocksIntegration(t *testing.T) {
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

	uniq := time.Now().UnixNano()
	mkUser := func(tag string) models.User {
		login := fmt.Sprintf("kn_%d_%s", uniq, tag)
		u, err := st.CreateUser(ctx, login, login, "x", models.RoleUser)
		if err != nil {
			t.Fatalf("CreateUser(%s): %v", tag, err)
		}
		return u
	}
	type side struct {
		pid   string
		round string
	}
	// match1x1 - завершённый матч из одного раунда; киллы и поправки вносятся до завершения.
	match1x1 := func(a, b models.User, play func(sa, sb side)) string {
		tour, err := st.CreateTournament(ctx, models.Tournament{Title: "kn-1x1", Mode: "1x1", RatingMultiplier: 1})
		if err != nil {
			t.Fatalf("CreateTournament: %v", err)
		}
		rd, err := st.CreateRound(ctx, models.Round{TournamentID: tour.ID, Number: 1, Status: "live"})
		if err != nil {
			t.Fatalf("CreateRound: %v", err)
		}
		pa, err := st.AddParticipant(ctx, models.Participant{TournamentID: tour.ID, Kind: "player", UserID: &a.ID, Name: a.Login, Seed: 1})
		if err != nil {
			t.Fatalf("AddParticipant a: %v", err)
		}
		pb, err := st.AddParticipant(ctx, models.Participant{TournamentID: tour.ID, Kind: "player", UserID: &b.ID, Name: b.Login, Seed: 2})
		if err != nil {
			t.Fatalf("AddParticipant b: %v", err)
		}
		play(side{pa.ID, rd.ID}, side{pb.ID, rd.ID})
		if _, err := st.UpdateTournamentStatus(ctx, tour.ID, "finished"); err != nil {
			t.Fatalf("UpdateTournamentStatus: %v", err)
		}
		return tour.ID
	}
	knock := func(s side, delta, wantApplied int) {
		t.Helper()
		got, err := st.AdjustKnocks(ctx, s.round, s.pid, delta)
		if err != nil {
			t.Fatalf("AdjustKnocks: %v", err)
		}
		if got != wantApplied {
			t.Errorf("AdjustKnocks(%d) = %d, want %d", delta, got, wantApplied)
		}
	}
	points := func(s side, delta, wantApplied int) {
		t.Helper()
		got, err := st.AdjustRoundPoints(ctx, s.round, s.pid, delta)
		if err != nil {
			t.Fatalf("AdjustRoundPoints: %v", err)
		}
		if got != wantApplied {
			t.Errorf("AdjustRoundPoints(%d) = %d, want %d", delta, got, wantApplied)
		}
	}
	entry := func(s side, wantPoints, wantKnocks int) {
		t.Helper()
		var p, k int
		if err := pool.QueryRow(ctx, `SELECT points, knocks FROM round_entries WHERE round_id = $1 AND participant_id = $2`,
			s.round, s.pid).Scan(&p, &k); err != nil {
			t.Fatalf("round_entries: %v", err)
		}
		if p != wantPoints || k != wantKnocks {
			t.Errorf("очки/киллы = %d/%d, want %d/%d", p, k, wantPoints, wantKnocks)
		}
	}
	knocksOf := func(u models.User) models.KnockStats {
		t.Helper()
		by, err := st.Player1x1KnocksBySeason(ctx, u.ID)
		if err != nil {
			t.Fatalf("Player1x1KnocksBySeason: %v", err)
		}
		var sum models.KnockStats
		for _, k := range by {
			sum.Heats += k.Heats
			sum.Knocks += k.Knocks
			if k.Best > sum.Best {
				sum.Best, sum.BestMatch, sum.BestOpponent, sum.BestMap = k.Best, k.BestMatch, k.BestOpponent, k.BestMap
			}
		}
		return sum
	}

	a, b, c := mkUser("a"), mkUser("b"), mkUser("c")

	// Килл приносит 3 очка, поправка не съедает очки за киллы, снять больше киллов, чем есть, нельзя.
	m1 := match1x1(a, b, func(sa, _ side) {
		knock(sa, 1, 1)
		knock(sa, 1, 1)
		knock(sa, 1, 1)
		entry(sa, 9, 3)
		points(sa, -1, 0)
		points(sa, 1, 1)
		points(sa, -1, -1)
		knock(sa, -5, -3)
		entry(sa, 0, 0)
		knock(sa, 1, 1)
		knock(sa, 1, 1)
		points(sa, 1, 1)
		entry(sa, 7, 2)
	})
	// Заходы без киллов, карты и места не сыграны - в статистику не идут.
	match1x1(a, c, func(_, _ side) {})
	// Заход с местом, но без киллов, сыгран - в зачёт с нулём; у лучшего захода видна его карта.
	m3 := match1x1(b, c, func(sb, sc side) {
		for range 4 {
			knock(sc, 1, 1)
		}
		if _, err := st.SetHeatMap(ctx, sc.round, sc.pid, "miramar"); err != nil {
			t.Fatalf("SetHeatMap: %v", err)
		}
		place := 3
		if _, err := st.SetHeatPlace(ctx, sb.round, sb.pid, &place); err != nil {
			t.Fatalf("SetHeatPlace: %v", err)
		}
	})

	if got, want := knocksOf(a), (models.KnockStats{Heats: 1, Knocks: 2, Best: 2, BestMatch: m1, BestOpponent: b.Login}); got != want {
		t.Errorf("киллы a = %+v, want %+v", got, want)
	}
	if got, want := knocksOf(b), (models.KnockStats{Heats: 1}); got != want {
		t.Errorf("киллы b = %+v, want %+v", got, want)
	}
	if got, want := knocksOf(c), (models.KnockStats{Heats: 1, Knocks: 4, Best: 4, BestMatch: m3, BestOpponent: b.Login, BestMap: "Мирамар"}); got != want {
		t.Errorf("киллы c = %+v, want %+v", got, want)
	}
}

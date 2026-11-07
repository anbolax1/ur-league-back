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

// Зачёт задания владельцу и сопернику независим: соперник получает +1, а владелец всё равно может
// выполнить своё задание. Запуск - как у TestMMRIntegration.
func TestTaskMarksIndependentIntegration(t *testing.T) {
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
	task, err := st.CreateCatalogTask(ctx, models.CatalogTask{
		Name: fmt.Sprintf("marks-%d", uniq), Text: "Задание для проверки зачётов.", Points: 3, ValueType: models.ValueFixed,
		Source: "official", Category: "task", Active: true,
	})
	if err != nil {
		t.Fatalf("CreateCatalogTask: %v", err)
	}
	defer func() { _ = st.DeleteCatalogTask(ctx, task.ID) }()

	tour, err := st.CreateTournament(ctx, models.Tournament{Title: "marks", Mode: "1x1"})
	if err != nil {
		t.Fatalf("CreateTournament: %v", err)
	}
	defer func() { _ = st.DeleteTournament(ctx, tour.ID) }()
	rd, err := st.CreateRound(ctx, models.Round{TournamentID: tour.ID, Number: 2, Status: "live"})
	if err != nil {
		t.Fatalf("CreateRound: %v", err)
	}
	var owner, rival string
	for i, name := range []string{"marks-a", "marks-b"} {
		p, err := st.AddParticipant(ctx, models.Participant{TournamentID: tour.ID, Name: name, Seed: i + 1})
		if err != nil {
			t.Fatalf("AddParticipant(%s): %v", name, err)
		}
		if i == 0 {
			owner = p.ID
		} else {
			rival = p.ID
		}
	}
	var asg string
	if err := pool.QueryRow(ctx,
		`INSERT INTO round_bonus_tasks (round_id, participant_id, task_id) VALUES ($1, $2, $3) RETURNING id`,
		rd.ID, owner, task.ID).Scan(&asg); err != nil {
		t.Fatalf("выдача задания: %v", err)
	}

	state := func() (completedBy, opponentBy string) {
		t.Helper()
		var c, o *string
		if err := pool.QueryRow(ctx, `SELECT completed_by, opponent_by FROM round_bonus_tasks WHERE id = $1`, asg).Scan(&c, &o); err != nil {
			t.Fatalf("чтение зачёта: %v", err)
		}
		name := func(id *string) string {
			switch {
			case id == nil:
				return "-"
			case *id == owner:
				return "владелец"
			case *id == rival:
				return "соперник"
			}
			return "чужой"
		}
		return name(c), name(o)
	}
	points := func() (own, opp int) {
		t.Helper()
		var err error
		if own, err = st.RecomputeParticipantPoints(ctx, owner); err != nil {
			t.Fatalf("очки владельца: %v", err)
		}
		if opp, err = st.RecomputeParticipantPoints(ctx, rival); err != nil {
			t.Fatalf("очки соперника: %v", err)
		}
		return own, opp
	}
	mark := func(by, wantC, wantO string, wantOwn, wantOpp int) {
		t.Helper()
		if _, err := st.MarkTask(ctx, asg, by); err != nil {
			t.Fatalf("MarkTask(%s): %v", by, err)
		}
		if c, o := state(); c != wantC || o != wantO {
			t.Errorf("после %q: исполнитель %s, соперник %s; want %s, %s", by, c, o, wantC, wantO)
		}
		if own, opp := points(); own != wantOwn || opp != wantOpp {
			t.Errorf("после %q: очки %d : %d, want %d : %d", by, own, opp, wantOwn, wantOpp)
		}
	}

	// Соперник засчитал чужое задание - владелец всё равно может выполнить своё, и очки идут обоим.
	mark("opponent", "соперник", "соперник", 0, 1)
	mark("owner", "владелец", "соперник", 3, 1)
	// Снятие одной стороны не трогает другую.
	mark("owner_off", "соперник", "соперник", 0, 1)
	mark("owner", "владелец", "соперник", 3, 1)
	mark("opponent_off", "владелец", "-", 3, 0)
	mark("none", "-", "-", 0, 0)

	// Отмена из журнала возвращает оба зачёта.
	res, err := st.MarkTask(ctx, asg, "opponent")
	if err != nil {
		t.Fatal(err)
	}
	res, err = st.MarkTask(ctx, asg, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMatchLog(ctx, tour.ID, 2, &owner, "task", "задание", 3, map[string]any{
		"type": "task", "assignmentId": asg, "prev": res.Prev, "target": res.Target,
		"prevOpp": res.PrevOpp, "targetOpp": res.TargetOpp, "hasOpp": true,
	}); err != nil {
		t.Fatalf("AddMatchLog: %v", err)
	}
	if _, err := st.UndoLastMatchLog(ctx, tour.ID); err != nil {
		t.Fatalf("UndoLastMatchLog: %v", err)
	}
	if c, o := state(); c != "соперник" || o != "соперник" {
		t.Errorf("после отмены: исполнитель %s, соперник %s; want соперник, соперник", c, o)
	}
}

// В матче на один раунд рейтинг меняется вдвое слабее, чем в матче из нескольких.
func TestSingleRoundHalfMmrIntegration(t *testing.T) {
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
	// gain - сколько набрал победитель матча из rounds раундов между двумя новыми игроками с равным рейтингом.
	gain := func(rounds int) int {
		t.Helper()
		var users [2]models.User
		for i := range users {
			login := fmt.Sprintf("sr_%d_%d_%d", uniq, rounds, i)
			u, err := st.CreateUser(ctx, login, login, "x", models.RoleUser)
			if err != nil {
				t.Fatalf("CreateUser: %v", err)
			}
			users[i] = u
		}
		tour, err := st.CreateTournament(ctx, models.Tournament{Title: fmt.Sprintf("sr-%d", rounds), Mode: "1x1", TotalRounds: rounds})
		if err != nil {
			t.Fatalf("CreateTournament: %v", err)
		}
		defer func() { _ = st.DeleteTournament(ctx, tour.ID) }()
		rd, err := st.CreateRound(ctx, models.Round{TournamentID: tour.ID, Number: 1, Status: "live"})
		if err != nil {
			t.Fatalf("CreateRound: %v", err)
		}
		var sides [2]string
		for i, u := range users {
			p, err := st.AddParticipant(ctx, models.Participant{TournamentID: tour.ID, Kind: "player", UserID: &u.ID, Name: u.Login, Seed: i + 1})
			if err != nil {
				t.Fatalf("AddParticipant: %v", err)
			}
			sides[i] = p.ID
		}
		if _, err := st.AdjustKnocks(ctx, rd.ID, sides[0], 1); err != nil {
			t.Fatalf("AdjustKnocks: %v", err)
		}
		if _, err := st.FinishMatch(ctx, tour.ID); err != nil {
			t.Fatalf("FinishMatch: %v", err)
		}
		var before, after int
		if err := pool.QueryRow(ctx, `SELECT mmr_before, mmr_after FROM mmr_history WHERE tournament_id = $1 AND user_id = $2`,
			tour.ID, users[0].ID).Scan(&before, &after); err != nil {
			t.Fatalf("mmr_history: %v", err)
		}
		return after - before
	}

	full, single := gain(2), gain(1)
	if full <= 0 || single*2 != full {
		t.Errorf("выигрыш за матч из двух раундов %d, за один раунд %d - ждём ровно вдвое меньше", full, single)
	}
}

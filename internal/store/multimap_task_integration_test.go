package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ur-league/backend/internal/db"
	"github.com/ur-league/backend/internal/models"
	"github.com/ur-league/backend/internal/store"
)

// Задание на двух картах выпадает на любой из них и в матче один раз, даже если играются обе карты;
// при смене карты захода меняется только задание на карту, отмена возвращает прежнее.
// Запуск - как у TestMMRIntegration.
func TestMultiMapTaskIntegration(t *testing.T) {
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

	// Свои карты без других заданий: в слот карты может выпасть только это задание.
	uniq := time.Now().UnixNano()
	maps := []string{fmt.Sprintf("mm_a_%d", uniq), fmt.Sprintf("mm_b_%d", uniq)}
	var tourID, taskID string
	defer func() {
		if tourID != "" {
			_ = st.DeleteTournament(ctx, tourID)
		}
		if taskID != "" {
			_ = st.DeleteCatalogTask(ctx, taskID)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM maps WHERE code = ANY($1)`, maps)
	}()

	for i, code := range maps {
		if _, err := pool.Exec(ctx, `INSERT INTO maps (code, name, sort_order) VALUES ($1, $1, $2)`, code, 1000+i); err != nil {
			t.Fatalf("карта %s: %v", code, err)
		}
	}
	task, err := st.CreateCatalogTask(ctx, models.CatalogTask{
		Name: fmt.Sprintf("mm-%d", uniq), Text: "Одно задание на две карты.", Points: 2, ValueType: models.ValueFixed,
		Source: "official", Category: "task", MapCodes: []string{maps[1], "no_such_map", maps[0], maps[1]}, Active: true,
	})
	if err != nil {
		t.Fatalf("CreateCatalogTask: %v", err)
	}
	taskID = task.ID
	if got := fmt.Sprint(task.MapCodes); got != fmt.Sprint(maps) {
		t.Fatalf("карты задания = %s, want %s (без лишних и повторов, в порядке карт)", got, fmt.Sprint(maps))
	}

	tour, err := st.CreateTournament(ctx, models.Tournament{Title: "mm-two-maps", Mode: "1x1"})
	if err != nil {
		t.Fatalf("CreateTournament: %v", err)
	}
	tourID = tour.ID
	var side string
	for i, name := range []string{"mm-a", "mm-b"} {
		p, err := st.AddParticipant(ctx, models.Participant{TournamentID: tour.ID, Name: name, Seed: i + 1})
		if err != nil {
			t.Fatalf("AddParticipant(%s): %v", name, err)
		}
		if i == 0 {
			side = p.ID
		}
	}
	mine := func() []string {
		t.Helper()
		dealt, err := st.ListTournamentBonusTasks(ctx, tour.ID)
		if err != nil {
			t.Fatalf("ListTournamentBonusTasks: %v", err)
		}
		var got []string
		for _, d := range dealt {
			if d.TaskID == task.ID {
				got = append(got, fmt.Sprintf("раунд %d, карта %s", d.RoundNumber, d.MapCode))
			}
		}
		return got
	}

	// Первый заход - на второй карте задания: подходит любая карта из списка, а не только первая.
	var roundIDs []string
	for i, code := range []string{maps[1], maps[0]} {
		rd, err := st.CreateRound(ctx, models.Round{TournamentID: tour.ID, Number: i + 1})
		if err != nil {
			t.Fatalf("CreateRound(%d): %v", i+1, err)
		}
		roundIDs = append(roundIDs, rd.ID)
		if err := st.DealRoundTasks(ctx, rd.ID); err != nil {
			t.Fatalf("DealRoundTasks(%d): %v", i+1, err)
		}
		if _, err := st.SetHeatMap(ctx, rd.ID, side, code); err != nil {
			t.Fatalf("SetHeatMap(%d): %v", i+1, err)
		}
	}
	if want := fmt.Sprintf("раунд 1, карта %s", maps[1]); fmt.Sprint(mine()) != fmt.Sprint([]string{want}) {
		t.Errorf("задание выдано: %v, want [%s]", mine(), want)
	}

	// Та же карта - ничего не меняется; другая - задание прежней карты снимается и выпадает заново.
	if ch, err := st.SetHeatMap(ctx, roundIDs[0], side, maps[1]); err != nil || ch.Changed {
		t.Fatalf("повтор той же карты: %+v, %v", ch, err)
	}
	ch, err := st.SetHeatMap(ctx, roundIDs[0], side, maps[0])
	if err != nil || !ch.Changed || ch.Prev != maps[1] || len(ch.Removed) != 1 || ch.Removed[0].TaskID != task.ID {
		t.Fatalf("смена карты: %+v, %v", ch, err)
	}
	if want := fmt.Sprintf("раунд 1, карта %s", maps[0]); fmt.Sprint(mine()) != fmt.Sprint([]string{want}) {
		t.Errorf("после смены карты: %v, want [%s]", mine(), want)
	}

	// Отмена из журнала возвращает прежнюю карту и прежнее задание под тем же id.
	if err := st.AddMatchLog(ctx, tour.ID, 1, &side, "map", "карта", 0, map[string]any{
		"type": "map", "roundId": roundIDs[0], "participantId": side, "prevMap": ch.Prev, "dealt": ch.Dealt, "removed": ch.Removed,
	}); err != nil {
		t.Fatalf("AddMatchLog: %v", err)
	}
	if _, err := st.UndoLastMatchLog(ctx, tour.ID); err != nil {
		t.Fatalf("UndoLastMatchLog: %v", err)
	}
	if want := fmt.Sprintf("раунд 1, карта %s", maps[1]); fmt.Sprint(mine()) != fmt.Sprint([]string{want}) {
		t.Errorf("после отмены: %v, want [%s]", mine(), want)
	}
	var back string
	if err := pool.QueryRow(ctx, `SELECT id FROM round_bonus_tasks WHERE round_id = $1 AND task_id = $2`, roundIDs[0], task.ID).Scan(&back); err != nil || back != ch.Removed[0].ID {
		t.Errorf("задание вернулось под id %q, want %q (%v)", back, ch.Removed[0].ID, err)
	}

	// Выполненное задание на карту не даёт сменить карту.
	if _, err := pool.Exec(ctx, `UPDATE round_bonus_tasks SET completed_by = $2 WHERE id = $1`, back, side); err != nil {
		t.Fatalf("зачёт: %v", err)
	}
	if _, err := st.SetHeatMap(ctx, roundIDs[0], side, maps[0]); !errors.Is(err, store.ErrConflict) {
		t.Errorf("смена карты после зачёта: %v, want ErrConflict", err)
	}
}

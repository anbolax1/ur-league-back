package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ur-league/backend/internal/db"
	"github.com/ur-league/backend/internal/store"
)

// Передача пояса организатором: запись в belt_transfers меняет носителя, владение прежнего
// кончается «hand», повторная передача тому же отклоняется.
func TestBeltTransferIntegration(t *testing.T) {
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

	stamp := time.Now().Format("150405.000000")
	a, _, err := st.CreatePlaceholderUser(ctx, "it-пояс-a-"+stamp)
	if err != nil {
		t.Fatalf("игрок a: %v", err)
	}
	b, _, err := st.CreatePlaceholderUser(ctx, "it-пояс-b-"+stamp)
	if err != nil {
		t.Fatalf("игрок b: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM belt_transfers WHERE from_user = ANY($1) OR to_user = ANY($1)`, []string{a.ID, b.ID})
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []string{a.ID, b.ID})
	}()

	tm, err := st.CreateMatch(ctx, store.NewMatch{Mode: "1x1", Format: store.FormatMatch, Rounds: 2,
		Sides: [2]store.MatchSide{{UserID: a.ID}, {UserID: b.ID}}})
	if err != nil {
		t.Fatalf("матч: %v", err)
	}
	defer func() { _ = st.DeleteTournament(ctx, tm.ID) }()
	if _, err := st.FinishMatch(ctx, tm.ID); err != nil {
		t.Fatalf("FinishMatch: %v", err)
	}
	// Без раундов матч кончается ничьей; для пояса нужен победитель.
	if _, err := pool.Exec(ctx, `
		UPDATE tournaments SET winner_participant_id = (
			SELECT id FROM participants WHERE tournament_id = $1 AND user_id = $2) WHERE id = $1`, tm.ID, a.ID); err != nil {
		t.Fatalf("победитель: %v", err)
	}

	belt, err := st.Belt(ctx)
	if err != nil {
		t.Fatalf("Belt: %v", err)
	}
	if belt.Holder == nil {
		t.Fatal("в тестовой базе пояс ничей - нужен хотя бы один матч с победителем")
	}
	if belt.Holder.UserID == b.ID {
		t.Fatal("пояс уже у b - передачу не проверить")
	}

	belt, err = st.TransferBelt(ctx, "", b.ID)
	if err != nil {
		t.Fatalf("TransferBelt: %v", err)
	}
	if belt.Holder == nil || belt.Holder.UserID != b.ID {
		t.Fatalf("пояс передан b: %+v", belt.Holder)
	}
	if n := len(belt.Reigns); n < 2 || belt.Reigns[n-2].End != "hand" || belt.Reigns[n-1].How != "hand" {
		t.Fatalf("владение прежнего носителя кончилось передачей, b взял пояс передачей: %+v", belt.Reigns[len(belt.Reigns)-1])
	}
	if belt.Reigns[len(belt.Reigns)-1].Opponent == nil || belt.Reigns[len(belt.Reigns)-1].Opponent.UserID == b.ID {
		t.Fatalf("у передачи указан прежний носитель: %+v", belt.Reigns[len(belt.Reigns)-1].Opponent)
	}

	if _, err := st.TransferBelt(ctx, "", b.ID); err != store.ErrBeltSame {
		t.Fatalf("повторная передача тому же: %v", err)
	}
	if _, err := st.TransferBelt(ctx, "", "нет-такого"); err != store.ErrNotFound {
		t.Fatalf("передача несуществующему: %v", err)
	}
}

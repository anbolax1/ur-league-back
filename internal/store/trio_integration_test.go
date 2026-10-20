package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ur-league/backend/internal/db"
	"github.com/ur-league/backend/internal/models"
	"github.com/ur-league/backend/internal/store"
)

// Составы 3×3 против реальной Postgres: матч, MMR команды, таблица своего формата и страница команды. Запуск:
//
//	UR_TEST_DB=postgres://ur:ur@localhost:5434/ur_league_it go test ./internal/store -run TestTrioTeams -v
//
// Без переменной - пропускается (в т.ч. в CI).
func TestTrioTeams(t *testing.T) {
	url := os.Getenv("UR_TEST_DB")
	if url == "" {
		t.Skip("UR_TEST_DB не задан - интеграционный тест пропущен")
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
	season, err := st.StartNewSeason(ctx, fmt.Sprintf("it-%d", uniq), 32, 1000)
	if err != nil {
		t.Fatalf("StartNewSeason: %v", err)
	}
	if err := st.RefreshMmrCaches(ctx); err != nil {
		t.Fatalf("RefreshMmrCaches: %v", err)
	}
	mkUser := func(tag string) models.User {
		login := fmt.Sprintf("it_%d_%s", uniq, tag)
		u, err := st.CreateUser(ctx, login, login, "x", models.RoleUser)
		if err != nil {
			t.Fatalf("CreateUser(%s): %v", tag, err)
		}
		return u
	}
	a1, a2, a3 := mkUser("a1"), mkUser("a2"), mkUser("a3")
	b1, b2, b3 := mkUser("b1"), mkUser("b2"), mkUser("b3")
	ids := func(us ...models.User) []string {
		out := make([]string, 0, len(us))
		for _, u := range us {
			out = append(out, u.ID)
		}
		return out
	}
	// Шоу-матч не ждёт, пока освободится эфир: тесту не мешают живые матчи в общей базе.
	at := time.Now().Add(time.Hour)
	newMatch := func(a, b []string) (models.Tournament, error) {
		return st.CreateMatch(ctx, store.NewMatch{Mode: "3x3", Format: store.FormatShow, StartsAt: &at,
			Sides: [2]store.MatchSide{{Members: a, Name: "Хищники"}, {Members: b}}})
	}

	if _, err := newMatch(ids(a1, a2, a3), ids(a1, b2, b3)); !errors.Is(err, store.ErrConflict) {
		t.Errorf("игрок в обеих сторонах: err = %v, want ErrConflict", err)
	}
	if _, err := newMatch(ids(a1, a2), ids(b1, b2, b3)); !errors.Is(err, store.ErrConflict) {
		t.Errorf("двое в составе 3×3: err = %v, want ErrConflict", err)
	}

	tour, err := newMatch(ids(a1, a2, a3), ids(b1, b2, b3))
	if err != nil {
		t.Fatalf("CreateMatch 3x3: %v", err)
	}
	if tour.Mode != "3x3" || len(tour.Participants) != 2 {
		t.Fatalf("матч: режим %q, сторон %d, want 3x3 и 2", tour.Mode, len(tour.Participants))
	}
	var win models.Participant
	for _, p := range tour.Participants {
		if p.Name == "Хищники" {
			win = p
		} else if want := strings.Join([]string{b1.Login, b2.Login, b3.Login}, " & "); p.Name != want {
			t.Errorf("название стороны без имени = %q, want %q", p.Name, want)
		}
	}
	if win.ID == "" {
		t.Fatalf("сторона «Хищники» не нашлась: %+v", tour.Participants)
	}
	if _, err := st.SetTournamentWinner(ctx, tour.ID, win.ID); err != nil {
		t.Fatalf("SetTournamentWinner: %v", err)
	}
	if err := st.ApplyTournamentMmr(ctx, tour.ID); err != nil {
		t.Fatalf("ApplyTournamentMmr: %v", err)
	}

	sorted := ids(a1, a2, a3)
	sort.Strings(sorted)
	key := strings.Join(sorted, "|")

	trio, err := st.TeamLeaderboard(ctx, "3x3", season.ID)
	if err != nil {
		t.Fatalf("TeamLeaderboard 3x3: %v", err)
	}
	var row *models.TeamLeaderboardRow
	for i := range trio {
		if trio[i].TeamKey == key {
			row = &trio[i]
		}
	}
	if row == nil || row.Mode != "3x3" || len(row.Members) != 3 || row.Mmr <= 1000 || row.Name != "Хищники" || row.Wins != 1 {
		t.Fatalf("строка победителя в таблице 3×3: %+v", row)
	}
	duo, err := st.TeamLeaderboard(ctx, "2x2", season.ID)
	if err != nil {
		t.Fatalf("TeamLeaderboard 2x2: %v", err)
	}
	for _, r := range duo {
		if r.TeamKey == key {
			t.Errorf("тройка попала в таблицу 2×2")
		}
	}

	if got, _ := st.BestTeamMmr(ctx, a2.ID, "3x3"); got != row.Mmr {
		t.Errorf("лучший MMR 3×3 игрока = %d, want %d", got, row.Mmr)
	}
	if got, _ := st.BestTeamMmr(ctx, a2.ID, "2x2"); got != 1000 {
		t.Errorf("лучший MMR 2×2 игрока без пар = %d, want стартовые 1000", got)
	}

	tp, ok, err := st.TeamProfile(ctx, key)
	if err != nil || !ok {
		t.Fatalf("TeamProfile: ok=%v err=%v", ok, err)
	}
	if tp.Mode != "3x3" || len(tp.Members) != 3 || tp.Stats.Place != 1 {
		t.Errorf("страница команды: формат %q, игроков %d, место %d, want 3x3, 3 и 1", tp.Mode, len(tp.Members), tp.Stats.Place)
	}
	if len(tp.Opponents) != 1 || strings.Count(tp.Opponents[0].Name, " & ") != 2 {
		t.Errorf("соперник на странице команды: %+v, want тройку через « & »", tp.Opponents)
	}

	teams, err := st.TeamsForUser(ctx, a3.ID)
	if err != nil {
		t.Fatalf("TeamsForUser: %v", err)
	}
	if len(teams) != 1 || teams[0].TeamKey != key || teams[0].Mode != "3x3" || len(teams[0].Members) != 3 {
		t.Errorf("команды игрока: %+v", teams)
	}
}

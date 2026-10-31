package store_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/ur-league/backend/internal/store"
)

// testTeam - команда, заведённая прямо в рейтинге: в лиге команда появляется после первого матча.
type testTeam struct {
	TeamKey string
}

// createTeam заводит команду 2×2 или 3×3 в team_mmr, как это делает первый сыгранный матч.
func createTeam(ctx context.Context, st *store.Store, name string, members ...string) (testTeam, error) {
	ids := append([]string(nil), members...)
	sort.Strings(ids)
	mode := "2x2"
	var third any
	if len(ids) == 3 {
		mode, third = "3x3", ids[2]
	}
	key := strings.Join(ids, "|")
	_, err := st.Pool.Exec(ctx, `
		INSERT INTO team_mmr (team_key, member_a, member_b, member_c, mode, mmr, name) VALUES ($1, $2, $3, $4, $5, 1000, $6)
		ON CONFLICT (team_key) DO UPDATE SET name = EXCLUDED.name`, key, ids[0], ids[1], third, mode, name)
	return testTeam{TeamKey: key}, err
}

var _ = testing.Short

package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/ur-league/backend/internal/models"
)

const roundCols = `id, tournament_id, number, map, status, COALESCE(map_code, '')`

func scanRound(row pgx.Row) (models.Round, error) {
	var r models.Round
	err := row.Scan(&r.ID, &r.TournamentID, &r.Number, &r.Map, &r.Status, &r.MapCode)
	return r, err
}

func (s *Store) ListRounds(ctx context.Context, tournamentID string) ([]models.Round, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT `+roundCols+` FROM rounds WHERE tournament_id = $1 ORDER BY number`,
		tournamentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Round{}
	for rows.Next() {
		r, err := scanRound(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreateRound(ctx context.Context, r models.Round) (models.Round, error) {
	const q = `
		INSERT INTO rounds (tournament_id, number, map, status)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tournament_id, number) DO UPDATE
			SET map = EXCLUDED.map, status = EXCLUDED.status
		RETURNING ` + roundCols
	if r.Status == "" {
		r.Status = "pending"
	}
	return scanRound(s.Pool.QueryRow(ctx, q, r.TournamentID, r.Number, r.Map, r.Status))
}

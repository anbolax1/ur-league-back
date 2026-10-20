package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/ur-league/backend/internal/models"
)

// ErrNotFound — запись не найдена (для отдачи 404 на уровне хендлера).
var ErrNotFound = errors.New("запись не найдена")

// ErrConflict — нарушение бизнес-правила (напр. задание уже выдано в этом матче).
var ErrConflict = errors.New("конфликт")

const participantCols = `id, tournament_id, kind, user_id, name, seed, total_points, members`

func scanParticipant(row pgx.Row) (models.Participant, error) {
	var p models.Participant
	var members []byte
	err := row.Scan(&p.ID, &p.TournamentID, &p.Kind, &p.UserID, &p.Name, &p.Seed, &p.TotalPoints, &members)
	if err != nil {
		return p, err
	}
	if len(members) > 0 {
		p.Members = json.RawMessage(members)
	} else {
		p.Members = json.RawMessage("[]")
	}
	return p, nil
}

func (s *Store) AddParticipant(ctx context.Context, p models.Participant) (models.Participant, error) {
	members := []byte("[]")
	if len(p.Members) > 0 {
		members = p.Members
	}
	if p.Kind == "" {
		p.Kind = "player"
	}
	const q = `
		INSERT INTO participants (tournament_id, kind, user_id, name, seed, members)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + participantCols
	return scanParticipant(s.Pool.QueryRow(ctx, q, p.TournamentID, p.Kind, p.UserID, p.Name, p.Seed, string(members)))
}

func (s *Store) ListParticipants(ctx context.Context, tournamentID string) ([]models.Participant, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT `+participantCols+` FROM participants WHERE tournament_id = $1 ORDER BY total_points DESC, seed ASC`,
		tournamentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Participant{}
	for rows.Next() {
		p, err := scanParticipant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetParticipant(ctx context.Context, id string) (models.Participant, error) {
	p, err := scanParticipant(s.Pool.QueryRow(ctx, `SELECT `+participantCols+` FROM participants WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

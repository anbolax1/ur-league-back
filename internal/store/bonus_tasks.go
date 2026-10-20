package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/ur-league/backend/internal/models"
)

// Задания сторон по раундам. Владелец задания = participant_id (кому выдано).
// completed_by — кто фактически выполнил: владелец → очки задания, противник → +1, NULL → не выполнено.
const rbtCols = `rbt.id, rbt.round_id, r.number, rbt.participant_id, rbt.task_id, ct.text, ct.points, ct.value_type, ct.kind, rbt.times, rbt.completed_by,
	ct.name, ct.category, ct.tag, ` + dealtTaskMap

// dealtTaskMap - карта выданного задания: у задания на картах это карта раунда, у общего - пусто.
const dealtTaskMap = `CASE WHEN cardinality(ct.map_codes) > 0 THEN COALESCE(r.map_code, '') ELSE '' END`

func scanRoundBonusTask(row pgx.Row) (models.RoundBonusTask, error) {
	var t models.RoundBonusTask
	err := row.Scan(&t.ID, &t.RoundID, &t.RoundNumber, &t.ParticipantID, &t.TaskID,
		&t.Text, &t.Points, &t.ValueType, &t.Kind, &t.Times, &t.CompletedBy,
		&t.Name, &t.Category, &t.Tag, &t.MapCode)
	return t, err
}

// ListTournamentBonusTasks — все контракты участников по раундам турнира.
func (s *Store) ListTournamentBonusTasks(ctx context.Context, tournamentID string) ([]models.RoundBonusTask, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT `+rbtCols+`
		FROM round_bonus_tasks rbt
		JOIN catalog_tasks ct ON ct.id = rbt.task_id
		JOIN rounds r ON r.id = rbt.round_id
		WHERE r.tournament_id = $1
		ORDER BY r.number, rbt.participant_id, ct.category, cardinality(ct.map_codes) > 0, ct.text`, tournamentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.RoundBonusTask{}
	for rows.Next() {
		t, err := scanRoundBonusTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// OpponentParticipant — другая сторона в раунде (для зачёта «выполнил контракт противника»).
func (s *Store) OpponentParticipant(ctx context.Context, roundID, participantID string) (string, error) {
	var oppID string
	err := s.Pool.QueryRow(ctx, `
		SELECT p.id FROM participants p
		JOIN rounds r ON r.tournament_id = p.tournament_id
		WHERE r.id = $1 AND p.id <> $2
		ORDER BY p.seed LIMIT 1`, roundID, participantID).Scan(&oppID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return oppID, err
}

// MarkResult - итог отметки задания: прежний и новый исполнитель и чьи очки пересчитать.
type MarkResult struct {
	RoundID  string
	Owner    string
	Prev     *string
	Target   *string
	Affected []string
}

// MarkTask - отметка исполнителя задания. Протокол засчитывается только владельцу.
func (s *Store) MarkTask(ctx context.Context, id, by string) (MarkResult, error) {
	var res MarkResult
	var category string
	err := s.Pool.QueryRow(ctx, `
		SELECT rbt.round_id, rbt.participant_id, rbt.completed_by, ct.category
		FROM round_bonus_tasks rbt JOIN catalog_tasks ct ON ct.id = rbt.task_id
		WHERE rbt.id = $1`, id).Scan(&res.RoundID, &res.Owner, &res.Prev, &category)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, ErrNotFound
	}
	if err != nil {
		return res, err
	}

	switch by {
	case "owner":
		// Зачёт владельцем имеет приоритет: ставится всегда и отменяет балл противника (перетирает completed_by).
		res.Target = &res.Owner
	case "opponent":
		// Протокол - личное задание, противнику его не засчитать. Задание засчитывается противнику,
		// ТОЛЬКО если владелец сам его не выполнил (иначе красть нечего).
		if category == "protocol" || (res.Prev != nil && *res.Prev == res.Owner) {
			return res, ErrConflict
		}
		opp, err := s.OpponentParticipant(ctx, res.RoundID, res.Owner)
		if err != nil {
			return res, err
		}
		res.Target = &opp
	case "none", "":
		res.Target = nil
	default:
		return res, ErrConflict
	}
	if err := s.SetTaskCompletedBy(ctx, id, res.Target); err != nil {
		return res, err
	}
	set := map[string]bool{}
	if res.Prev != nil {
		set[*res.Prev] = true
	}
	if res.Target != nil {
		set[*res.Target] = true
	}
	for k := range set {
		res.Affected = append(res.Affected, k)
	}
	return res, nil
}

// SetTaskCompletedBy ставит исполнителя задания напрямую (для отмены из журнала).
func (s *Store) SetTaskCompletedBy(ctx context.Context, id string, by *string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE round_bonus_tasks SET completed_by = $2 WHERE id = $1`, id, by)
	return err
}

// GetBonusAssignment - выданное задание и матч, к которому оно относится.
func (s *Store) GetBonusAssignment(ctx context.Context, id string) (models.RoundBonusTask, string, error) {
	var tournamentID string
	row := s.Pool.QueryRow(ctx, `
		SELECT `+rbtCols+`, r.tournament_id
		FROM round_bonus_tasks rbt
		JOIN catalog_tasks ct ON ct.id = rbt.task_id
		JOIN rounds r ON r.id = rbt.round_id
		WHERE rbt.id = $1`, id)
	var t models.RoundBonusTask
	err := row.Scan(&t.ID, &t.RoundID, &t.RoundNumber, &t.ParticipantID, &t.TaskID,
		&t.Text, &t.Points, &t.ValueType, &t.Kind, &t.Times, &t.CompletedBy,
		&t.Name, &t.Category, &t.Tag, &t.MapCode, &tournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, "", ErrNotFound
	}
	return t, tournamentID, err
}

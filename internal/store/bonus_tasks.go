package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/ur-league/backend/internal/models"
)

// Задания сторон по раундам. Владелец задания = participant_id (кому выдано).
// completed_by — владелец, если он выполнил (очки задания), иначе противник, иначе NULL; opponent_by — противник,
// засчитавший чужое задание (+1): он может стоять и вместе с выполнением владельца.
const rbtCols = `rbt.id, rbt.round_id, r.number, rbt.participant_id, rbt.task_id, ct.text, ct.points, ct.value_type, rbt.times, rbt.completed_by, rbt.opponent_by,
	ct.name, ct.category, ct.tag, ` + dealtTaskMap

// dealtTaskMap - карта выданного задания: у задания на картах это карта захода стороны, у общего - пусто.
const dealtTaskMap = `CASE WHEN cardinality(ct.map_codes) > 0 THEN COALESCE(re.map_code, '') ELSE '' END`

// heatJoin - заход владельца задания, из него берётся карта задания.
const heatJoin = `LEFT JOIN round_entries re ON re.round_id = rbt.round_id AND re.participant_id = rbt.participant_id`

func scanRoundBonusTask(row pgx.Row) (models.RoundBonusTask, error) {
	var t models.RoundBonusTask
	err := row.Scan(&t.ID, &t.RoundID, &t.RoundNumber, &t.ParticipantID, &t.TaskID,
		&t.Text, &t.Points, &t.ValueType, &t.Times, &t.CompletedBy, &t.OpponentBy,
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
		`+heatJoin+`
		WHERE r.tournament_id = $1
		ORDER BY r.number, rbt.participant_id, ct.category, rbt.map_slot, ct.text`, tournamentID)
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

// MarkResult - итог отметки задания: прежнее и новое состояние зачёта и чьи очки пересчитать.
type MarkResult struct {
	RoundID   string
	Owner     string
	Prev      *string // completed_by до отметки
	Target    *string // completed_by после
	PrevOpp   *string // opponent_by до отметки
	TargetOpp *string // opponent_by после
	Affected  []string
}

// MarkTask - отметка выполнения задания. Владелец и соперник засчитываются независимо: соперник
// получает +1, а владелец всё равно может выполнить своё задание. Протокол засчитывается только владельцу.
// by: owner / opponent - засчитать; owner_off / opponent_off - снять только эту сторону; none - снять всё.
func (s *Store) MarkTask(ctx context.Context, id, by string) (MarkResult, error) {
	var res MarkResult
	var category string
	err := s.Pool.QueryRow(ctx, `
		SELECT rbt.round_id, rbt.participant_id, rbt.completed_by, rbt.opponent_by, ct.category
		FROM round_bonus_tasks rbt JOIN catalog_tasks ct ON ct.id = rbt.task_id
		WHERE rbt.id = $1`, id).Scan(&res.RoundID, &res.Owner, &res.Prev, &res.PrevOpp, &category)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, ErrNotFound
	}
	if err != nil {
		return res, err
	}

	ownerDone := res.Prev != nil && *res.Prev == res.Owner
	oppBy := res.PrevOpp
	switch by {
	case "owner":
		ownerDone = true
	case "owner_off":
		ownerDone = false
	case "opponent":
		// Протокол - личное задание, противнику его не засчитать.
		if category == "protocol" {
			return res, ErrConflict
		}
		opp, err := s.OpponentParticipant(ctx, res.RoundID, res.Owner)
		if err != nil {
			return res, err
		}
		oppBy = &opp
	case "opponent_off":
		oppBy = nil
	case "none", "":
		ownerDone, oppBy = false, nil
	default:
		return res, ErrConflict
	}
	res.Target, res.TargetOpp = taskCompletedBy(res.Owner, ownerDone, oppBy), oppBy
	if err := s.SetTaskDone(ctx, id, res.Target, res.TargetOpp); err != nil {
		return res, err
	}
	set := map[string]bool{}
	for _, p := range []*string{res.Prev, res.PrevOpp, res.Target, res.TargetOpp} {
		if p != nil {
			set[*p] = true
		}
	}
	for k := range set {
		res.Affected = append(res.Affected, k)
	}
	return res, nil
}

// taskCompletedBy - значение completed_by: владелец, если он выполнил, иначе соперник, засчитавший задание.
func taskCompletedBy(owner string, ownerDone bool, oppBy *string) *string {
	if ownerDone {
		return &owner
	}
	return oppBy
}

// SetTaskDone ставит зачёт задания напрямую (для отмены из журнала).
func (s *Store) SetTaskDone(ctx context.Context, id string, completedBy, opponentBy *string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE round_bonus_tasks SET completed_by = $2, opponent_by = $3 WHERE id = $1`, id, completedBy, opponentBy)
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
		`+heatJoin+`
		WHERE rbt.id = $1`, id)
	var t models.RoundBonusTask
	err := row.Scan(&t.ID, &t.RoundID, &t.RoundNumber, &t.ParticipantID, &t.TaskID,
		&t.Text, &t.Points, &t.ValueType, &t.Times, &t.CompletedBy, &t.OpponentBy,
		&t.Name, &t.Category, &t.Tag, &t.MapCode, &tournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, "", ErrNotFound
	}
	return t, tournamentID, err
}

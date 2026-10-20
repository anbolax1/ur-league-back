package store

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/ur-league/backend/internal/models"
)

// ErrMatchStarted - очерёдность сторон уже не поменять: первый раунд начался.
var ErrMatchStarted = errors.New("матч уже начался")

// ListMaps - действующие карты справочника по порядку.
func (s *Store) ListMaps(ctx context.Context) ([]models.MapInfo, error) {
	rows, err := s.Pool.Query(ctx, `SELECT code, name, image, sort_order, added_at FROM maps WHERE active ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.MapInfo{}
	for rows.Next() {
		var m models.MapInfo
		if err := rows.Scan(&m.Code, &m.Name, &m.Image, &m.SortOrder, &m.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Форматы матча: обычный матч и шоу-матч, объявленный заранее.
const (
	FormatMatch = "match"
	FormatShow  = "show"
)

// MatchRounds - сколько раундов будет в новом матче: у шоу-матча всегда три, обычный - два или три.
func MatchRounds(format string, rounds int) int {
	if format == FormatShow || rounds == 3 {
		return 3
	}
	return 2
}

// roundsStarted - начался ли хоть один раунд (после этого очерёдность сторон не меняется).
func roundsStarted(ctx context.Context, tx pgx.Tx, tournamentID string) (bool, error) {
	var started bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM rounds WHERE tournament_id = $1 AND status <> 'pending')`, tournamentID).Scan(&started)
	return started, err
}

// SetFirstSide делает сторону participantID стороной A - в каждом раунде она заходит первой.
// Так исправляют перепутанный жребий, пока матч не начался.
func (s *Store) SetFirstSide(ctx context.Context, tournamentID, participantID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if started, err := roundsStarted(ctx, tx, tournamentID); err != nil {
		return err
	} else if started {
		return ErrMatchStarted
	}
	rows, err := tx.Query(ctx, `
		SELECT id, name FROM participants WHERE tournament_id = $1 ORDER BY seed, id FOR UPDATE`, tournamentID)
	if err != nil {
		return err
	}
	var ids, names []string
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return err
		}
		ids, names = append(ids, id), append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	i := slices.Index(ids, participantID)
	switch {
	case i < 0:
		return ErrNotFound
	case len(ids) != 2:
		return ErrConflict
	case i == 0:
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE participants SET seed = CASE WHEN id = $2 THEN 1 ELSE 2 END WHERE tournament_id = $1`,
		tournamentID, participantID); err != nil {
		return err
	}
	// Название матча собрано из ников в порядке сторон; написанное вручную не трогаем.
	if _, err := tx.Exec(ctx, `
		UPDATE tournaments SET title = CASE WHEN title = $2 THEN $3 ELSE title END, updated_at = now() WHERE id = $1`,
		tournamentID, names[0]+" vs "+names[1], names[1]+" vs "+names[0]); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Места в матче Ultimate Royale: 16 команд.
const (
	MinPlace = 1
	MaxPlace = 16
)

// SetHeatPlace записывает место стороны в матче PUBG по экрану итогов захода; nil - стереть. Отдаёт прежнее
// место - для отмены из журнала.
func (s *Store) SetHeatPlace(ctx context.Context, roundID, participantID string, place *int) (*int, error) {
	var prev *int
	err := s.Pool.QueryRow(ctx,
		`SELECT place FROM round_entries WHERE round_id = $1 AND participant_id = $2`, roundID, participantID).Scan(&prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO round_entries (round_id, participant_id, place) VALUES ($1, $2, $3)
		ON CONFLICT (round_id, participant_id) DO UPDATE SET place = EXCLUDED.place, updated_at = now()`,
		roundID, participantID, place)
	return prev, err
}

// HeatTask - задание на карту, выданное заходу: его id и задание каталога. Нужно, чтобы отмена
// вернула прежнее задание под тем же id.
type HeatTask struct {
	ID     string `json:"id"`
	TaskID string `json:"taskId"`
}

// HeatMapChange - что поменяла отметка карты захода: прежняя карта, снятые задания прежней карты
// и выданное задание новой. Changed=false - карта та же, менять нечего.
type HeatMapChange struct {
	Changed bool
	MapName string
	Prev    string
	Removed []HeatTask
	Dealt   string
}

// SetHeatMap отмечает карту, которую выбрала игра для захода стороны в раунде, и выдаёт задание на эту
// карту, а если на неё заданий нет - ещё одно общее. Задание прежней карты снимается; выполненное
// снять нельзя - ErrConflict, сначала отменяют зачёт.
func (s *Store) SetHeatMap(ctx context.Context, roundID, participantID, mapCode string) (HeatMapChange, error) {
	var ch HeatMapChange
	if err := s.Pool.QueryRow(ctx, `SELECT name FROM maps WHERE code = $1 AND active`, mapCode).Scan(&ch.MapName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ch, ErrNotFound
		}
		return ch, err
	}
	var tournamentID string
	if err := s.Pool.QueryRow(ctx, `SELECT tournament_id FROM rounds WHERE id = $1`, roundID).Scan(&tournamentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ch, ErrNotFound
		}
		return ch, err
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ch, err
	}
	defer tx.Rollback(ctx)
	var prev *string
	err = tx.QueryRow(ctx, `
		SELECT map_code FROM round_entries WHERE round_id = $1 AND participant_id = $2 FOR UPDATE`,
		roundID, participantID).Scan(&prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ch, err
	}
	if prev != nil {
		ch.Prev = *prev
	}
	if ch.Prev == mapCode {
		return ch, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id, task_id, completed_by IS NOT NULL FROM round_bonus_tasks
		WHERE round_id = $1 AND participant_id = $2 AND map_slot`, roundID, participantID)
	if err != nil {
		return ch, err
	}
	for rows.Next() {
		var t HeatTask
		var done bool
		if err := rows.Scan(&t.ID, &t.TaskID, &done); err != nil {
			rows.Close()
			return ch, err
		}
		if done {
			rows.Close()
			return ch, ErrConflict
		}
		ch.Removed = append(ch.Removed, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ch, err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM round_bonus_tasks WHERE round_id = $1 AND participant_id = $2 AND map_slot`, roundID, participantID); err != nil {
		return ch, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO round_entries (round_id, participant_id, map_code) VALUES ($1, $2, $3)
		ON CONFLICT (round_id, participant_id) DO UPDATE SET map_code = EXCLUDED.map_code, updated_at = now()`,
		roundID, participantID, mapCode); err != nil {
		return ch, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ch, err
	}
	ch.Changed = true

	id, err := s.pickTask(ctx, tournamentID, taskSlot{"task", true}, mapCode, "")
	if errors.Is(err, ErrNotFound) {
		id, err = s.pickTask(ctx, tournamentID, taskSlot{"task", false}, "", "")
	}
	if errors.Is(err, ErrNotFound) {
		return ch, nil
	}
	if err != nil {
		return ch, err
	}
	if err := s.Pool.QueryRow(ctx, `
		INSERT INTO round_bonus_tasks (round_id, participant_id, task_id, map_slot) VALUES ($1, $2, $3, true)
		ON CONFLICT (round_id, participant_id, task_id) DO NOTHING
		RETURNING id`, roundID, participantID, id).Scan(&ch.Dealt); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ch, err
	}
	return ch, nil
}

// undoHeatMap возвращает заходу прежнюю карту вместе с её заданием и снимает выданное по новой.
func (s *Store) undoHeatMap(ctx context.Context, roundID, participantID, prev, dealt string, removed []HeatTask) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if dealt != "" {
		if _, err := tx.Exec(ctx, `DELETE FROM round_bonus_tasks WHERE id = $1`, dealt); err != nil {
			return err
		}
	}
	for _, t := range removed {
		if _, err := tx.Exec(ctx, `
			INSERT INTO round_bonus_tasks (id, round_id, participant_id, task_id, map_slot) VALUES ($1, $2, $3, $4, true)
			ON CONFLICT DO NOTHING`, t.ID, roundID, participantID, t.TaskID); err != nil {
			return err
		}
	}
	var code *string
	if prev != "" {
		code = &prev
	}
	if _, err := tx.Exec(ctx, `
		UPDATE round_entries SET map_code = $3, updated_at = now() WHERE round_id = $1 AND participant_id = $2`,
		roundID, participantID, code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

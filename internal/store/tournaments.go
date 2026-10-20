package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/ur-league/backend/internal/models"
)

const tournamentCols = `id, title, mode, status, total_rounds, maps, starts_at, winner_participant_id, created_at, updated_at, rating_multiplier, format, games, prize, preview_path`

// tournamentFields - куда читать колонки tournamentCols; finish доводит прочитанное до модели.
func tournamentFields(t *models.Tournament) (dest []any, finish func()) {
	var mapsRaw []byte
	var preview string
	dest = []any{&t.ID, &t.Title, &t.Mode, &t.Status, &t.TotalRounds, &mapsRaw,
		&t.StartsAt, &t.WinnerParticipantID, &t.CreatedAt, &t.UpdatedAt, &t.RatingMultiplier, &t.Format, &t.Games,
		&t.Prize, &preview}
	finish = func() {
		if len(mapsRaw) > 0 {
			_ = json.Unmarshal(mapsRaw, &t.Maps)
		}
		if t.Maps == nil {
			t.Maps = []string{}
		}
		if preview != "" {
			t.PreviewURL = "/media/" + preview
		}
	}
	return dest, finish
}

func scanTournament(row pgx.Row) (models.Tournament, error) {
	var t models.Tournament
	dest, finish := tournamentFields(&t)
	if err := row.Scan(dest...); err != nil {
		return t, err
	}
	finish()
	return t, nil
}

func (s *Store) CreateTournament(ctx context.Context, t models.Tournament) (models.Tournament, error) {
	maps := []byte("[]")
	if t.Maps != nil {
		maps, _ = json.Marshal(t.Maps)
	}
	if t.Mode == "" {
		t.Mode = "1x1"
	}
	if t.Status == "" {
		t.Status = "upcoming"
	}
	// Ровно 1 раунд на турнир (один раунд = один рейд) — менять нельзя.
	t.TotalRounds = 1
	if t.RatingMultiplier < 1 {
		t.RatingMultiplier = 1
	}
	// season_id — текущий активный сезон (подзапросом): новые турниры идут в него.
	const q = `
		INSERT INTO tournaments (title, mode, status, total_rounds, maps, starts_at, rating_multiplier, season_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, (SELECT id FROM seasons WHERE status = 'active' LIMIT 1))
		RETURNING ` + tournamentCols
	created, err := scanTournament(s.Pool.QueryRow(ctx, q, t.Title, t.Mode, t.Status, t.TotalRounds, string(maps), t.StartsAt, t.RatingMultiplier))
	if err != nil {
		return created, err
	}
	// Авто-создаём единственный раунд (карта — первая из списка, если есть).
	roundMap := ""
	if len(t.Maps) > 0 {
		roundMap = t.Maps[0]
	}
	if _, err := s.CreateRound(ctx, models.Round{TournamentID: created.ID, Number: 1, Map: roundMap, Status: "pending"}); err != nil {
		return created, err
	}
	created.Rounds, _ = s.ListRounds(ctx, created.ID)
	return created, nil
}

// DeleteTournament удаляет матч вместе с его сторонами, раундами и выданными заданиями; каталог
// заданий не трогается. Поставленных в матч игроков возвращает в общий пул заявок.
func (s *Store) DeleteTournament(ctx context.Context, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Вернуть в пул тех, кто был поставлен в этот турнир из заявки (иначе их заявка
	// осталась бы «accepted» без турнира — registrations.tournament_id здесь ON DELETE SET NULL).
	if _, err := tx.Exec(ctx,
		`UPDATE registrations SET status = 'pending', tournament_id = NULL, decided_at = NULL
		   WHERE tournament_id = $1 AND status = 'accepted'`, id); err != nil {
		return err
	}
	ct, err := tx.Exec(ctx, `DELETE FROM tournaments WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// ListTournaments - матчи для списков: вместо полного состава - стороны со счётом и карты раундов.
// У сыгранного матча - только начатых раундов: при досрочном завершении следующая карта не сыграна.
func (s *Store) ListTournaments(ctx context.Context, status string) ([]models.Tournament, error) {
	q := `SELECT ` + tournamentCols + `,
		(SELECT COUNT(*) FROM participants WHERE tournament_id = tournaments.id),
		CASE WHEN tournaments.mode IN ('2x2', '3x3') THEN (
			(SELECT COUNT(*) FROM participants p WHERE p.tournament_id = tournaments.id) < 2
			OR EXISTS (SELECT 1 FROM participants p WHERE p.tournament_id = tournaments.id
			           AND jsonb_array_length(p.members) < CASE tournaments.mode WHEN '3x3' THEN 3 ELSE 2 END)
		) ELSE (
			(SELECT COUNT(*) FROM participants p WHERE p.tournament_id = tournaments.id) < 2
		) END,
		(SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'name', p.name, 'points', p.total_points,
			'winner', COALESCE(p.id = tournaments.winner_participant_id, false)) ORDER BY p.seed), '[]'::jsonb)
		 FROM participants p WHERE p.tournament_id = tournaments.id),
		COALESCE((SELECT jsonb_agg(m.name ORDER BY r.number, p.seed) FROM round_entries re
		          JOIN rounds r ON r.id = re.round_id JOIN participants p ON p.id = re.participant_id
		          JOIN maps m ON m.code = re.map_code
		          WHERE r.tournament_id = tournaments.id), tournaments.maps, '[]'::jsonb)
		FROM tournaments`
	args := []any{}
	if status != "" {
		q += ` WHERE status = $1`
		args = append(args, status)
	}
	q += ` ORDER BY COALESCE(starts_at, created_at) DESC`

	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Tournament{}
	for rows.Next() {
		var t models.Tournament
		var scoreRaw, mapsRaw []byte
		dest, finish := tournamentFields(&t)
		if err := rows.Scan(append(dest, &t.ParticipantCount, &t.HasSpace, &scoreRaw, &mapsRaw)...); err != nil {
			return nil, err
		}
		finish()
		if err := json.Unmarshal(scoreRaw, &t.Score); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(mapsRaw, &t.RoundMaps); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTournament(ctx context.Context, id string) (models.Tournament, error) {
	t, err := scanTournament(s.Pool.QueryRow(ctx, `SELECT `+tournamentCols+` FROM tournaments WHERE id = $1`, id))
	if err != nil {
		return t, err
	}
	if t.Participants, err = s.ListParticipants(ctx, id); err != nil {
		return t, err
	}
	t.ParticipantCount = len(t.Participants)
	if t.Rounds, err = s.ListRounds(ctx, id); err != nil {
		return t, err
	}
	s.PopulateTournamentMmrChanges(ctx, &t)
	return t, nil
}

// TournamentStatus возвращает только статус турнира (для проверок).
func (s *Store) TournamentStatus(ctx context.Context, id string) (string, error) {
	var status string
	err := s.Pool.QueryRow(ctx, `SELECT status FROM tournaments WHERE id = $1`, id).Scan(&status)
	return status, err
}

func (s *Store) UpdateTournamentStatus(ctx context.Context, id, status string) (models.Tournament, error) {
	return scanTournament(s.Pool.QueryRow(ctx,
		`UPDATE tournaments SET status = $2, updated_at = now() WHERE id = $1 RETURNING `+tournamentCols,
		id, status))
}

func (s *Store) SetTournamentWinner(ctx context.Context, id, participantID string) (models.Tournament, error) {
	return scanTournament(s.Pool.QueryRow(ctx,
		`UPDATE tournaments SET winner_participant_id = $2, status = 'finished', updated_at = now()
		 WHERE id = $1 RETURNING `+tournamentCols,
		id, participantID))
}

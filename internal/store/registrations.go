package store

import (
	"context"
	"time"

	"github.com/ur-league/backend/internal/models"
)

// priorityToday - игрок сегодня активировал «Без очереди»: его заявка в пуле выше остальных до конца
// игрового дня. $1 - начало сегодняшнего дня по Москве.
const priorityToday = `EXISTS (SELECT 1 FROM token_ledger l WHERE l.user_id = r.user_id AND l.kind = 'priority'
	AND l.action = 'use' AND l.created_at >= $1)`

// regCols - колонки заявки в том порядке, в котором их читает scanRegistration.
const regCols = `r.id, r.tournament_id, r.user_id, r.game_id, r.game_nick, r.status, r.note, r.rank, r.kd, r.matches, r.wins, r.kills,
	r.play_time, r.created_at, r.decided_at`

func regDest(r *models.Registration) []any {
	return []any{&r.ID, &r.TournamentID, &r.UserID, &r.GameID, &r.GameNick, &r.Status, &r.Note, &r.Rank, &r.Kd, &r.Matches, &r.Wins,
		&r.Kills, &r.PlayTime, &r.CreatedAt, &r.DecidedAt}
}

// RegistrationForm - анкета заявки: что игрок написал о себе.
type RegistrationForm struct {
	GameID, GameNick, Rank, PlayTime, Note string
	Kd                                     *float64
	Matches, Wins, Kills                   *int
}

// CreateRegistration создаёт/обновляет заявку игрока в общий пул (одна на пользователя).
// Повторная подача обновляет анкету и возвращает заявку в пул (status=pending).
func (s *Store) CreateRegistration(ctx context.Context, userID string, f RegistrationForm) (models.Registration, error) {
	const q = `
		INSERT INTO registrations (user_id, game_id, game_nick, note, rank, kd, matches, wins, kills, play_time)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (user_id) DO UPDATE
			SET game_id = EXCLUDED.game_id, game_nick = EXCLUDED.game_nick, note = EXCLUDED.note,
			    rank = EXCLUDED.rank, kd = EXCLUDED.kd, matches = EXCLUDED.matches, wins = EXCLUDED.wins,
			    kills = EXCLUDED.kills, play_time = EXCLUDED.play_time,
			    status = 'pending', tournament_id = NULL, decided_at = NULL,
			    created_at = now()
		RETURNING id, tournament_id, user_id, game_id, game_nick, status, note, rank, kd, matches, wins, kills, play_time,
		          created_at, decided_at`
	var r models.Registration
	err := s.Pool.QueryRow(ctx, q, userID, f.GameID, f.GameNick, f.Note, f.Rank, f.Kd, f.Matches, f.Wins, f.Kills, f.PlayTime).
		Scan(regDest(&r)...)
	return r, err
}

func (s *Store) GetRegistration(ctx context.Context, id string) (models.Registration, error) {
	const q = `
		SELECT ` + regCols + `,
		       u.login, u.display_name, u.avatar_url
		FROM registrations r
		JOIN users u ON u.id = r.user_id
		WHERE r.id = $1`
	var r models.Registration
	err := s.Pool.QueryRow(ctx, q, id).Scan(append(regDest(&r), &r.UserLogin, &r.UserDisplayName, &r.UserAvatarURL)...)
	return r, err
}

func (s *Store) ListMyRegistrations(ctx context.Context, userID string) ([]models.Registration, error) {
	const q = `
		SELECT ` + regCols + `,
		       t.title
		FROM registrations r
		LEFT JOIN tournaments t ON t.id = r.tournament_id
		WHERE r.user_id = $1
		ORDER BY r.created_at DESC`
	rows, err := s.Pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Registration{}
	for rows.Next() {
		var r models.Registration
		var title *string
		if err := rows.Scan(append(regDest(&r), &title)...); err != nil {
			return nil, err
		}
		if title != nil {
			r.TournamentTitle = *title
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPool - открытые заявки (ещё не распределённые): сначала «Без очереди», дальше кто подал раньше.
func (s *Store) ListPool(ctx context.Context) ([]models.Registration, error) {
	const q = `
		SELECT ` + regCols + `,
		       u.login, u.display_name, u.avatar_url, ` + priorityToday + ` AS priority
		FROM registrations r
		JOIN users u ON u.id = r.user_id
		WHERE r.status = 'pending'
		ORDER BY priority DESC, r.created_at ASC`
	rows, err := s.Pool.Query(ctx, q, mskDay(time.Now()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Registration{}
	for rows.Next() {
		var r models.Registration
		if err := rows.Scan(append(regDest(&r), &r.UserLogin, &r.UserDisplayName, &r.UserAvatarURL, &r.Priority)...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPoolPage — страница открытых заявок (пагинация для бесконечной подгрузки).
// total — сколько всего pending-заявок (для индикатора «загружено N из total»).
func (s *Store) ListPoolPage(ctx context.Context, limit, offset int) ([]models.Registration, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}
	const q = `
		SELECT ` + regCols + `,
		       u.login, u.display_name, u.avatar_url, ` + priorityToday + ` AS priority,
		       COUNT(*) OVER() AS total
		FROM registrations r
		JOIN users u ON u.id = r.user_id
		WHERE r.status = 'pending'
		ORDER BY priority DESC, r.created_at ASC
		LIMIT $2 OFFSET $3`
	rows, err := s.Pool.Query(ctx, q, mskDay(time.Now()), limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []models.Registration{}
	total := 0
	for rows.Next() {
		var r models.Registration
		if err := rows.Scan(append(regDest(&r), &r.UserLogin, &r.UserDisplayName, &r.UserAvatarURL, &r.Priority, &total)...); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// SyncPendingRegistrationGame — обновляет игровой ID и ник в незакрытой (pending) заявке игрока,
// чтобы пул показывал актуальные после смены в профиле. У принятых/отклонённых заявок остаётся
// снимок на момент решения.
func (s *Store) SyncPendingRegistrationGame(ctx context.Context, userID, gameID, gameNick string) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE registrations SET game_id = $2, game_nick = $3 WHERE user_id = $1 AND status = 'pending'`,
		userID, gameID, gameNick)
	return err
}

// MarkRegistrationPlaced — игрок поставлен в турнир: заявка принята и уходит из пула.
// No-op, если у пользователя нет открытой заявки.
func (s *Store) MarkRegistrationPlaced(ctx context.Context, userID, tournamentID string) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE registrations SET status = 'accepted', tournament_id = $2, decided_at = now()
		   WHERE user_id = $1 AND status = 'pending'`, userID, tournamentID)
	return err
}

// DecideRegistration — отклонить/принять заявку вручную (используется для отклонения из пула).
func (s *Store) DecideRegistration(ctx context.Context, id, status string) (models.Registration, error) {
	_, err := s.Pool.Exec(ctx,
		`UPDATE registrations SET status = $2, decided_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return models.Registration{}, err
	}
	return s.GetRegistration(ctx, id)
}

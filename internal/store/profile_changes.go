package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/ur-league/backend/internal/models"
)

// Виды заявок на смену профиля.
const (
	ChangeAvatar = "avatar"
	ChangeName   = "name"
)

var (
	ErrNameTaken     = errors.New("такой ник уже занят")
	ErrChangeDecided = errors.New("заявка уже рассмотрена")
)

// mediaPrefix - адрес картинок из хранилища медиа на сайте.
const mediaPrefix = "/media/"

const changeCols = `c.id, c.user_id, c.kind, c.value, c.status, c.reason, c.created_at, c.decided_at,
	u.login, u.display_name, u.avatar_url`

func scanChange(row pgx.Row) (models.ProfileChange, error) {
	var c models.ProfileChange
	err := row.Scan(&c.ID, &c.UserID, &c.Kind, &c.Value, &c.Status, &c.Reason, &c.CreatedAt, &c.DecidedAt,
		&c.UserLogin, &c.UserDisplayName, &c.UserAvatarURL)
	if c.Kind == ChangeAvatar {
		c.Value = mediaPrefix + c.Value
	}
	return c, err
}

// nameFree - ник не совпадает с чужим логином или ником (без учёта регистра).
func (s *Store) nameFree(ctx context.Context, userID, name string) (bool, error) {
	var taken bool
	err := s.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE id <> $1 AND (lower(login) = lower($2) OR lower(display_name) = lower($2)))`,
		userID, name).Scan(&taken)
	return !taken, err
}

// SubmitProfileChange кладёт заявку игрока на проверку; прежняя нерассмотренная того же вида заменяется.
// replaced - файл заменённой заявки на фото: его можно удалить из хранилища.
func (s *Store) SubmitProfileChange(ctx context.Context, userID, kind, value string) (change models.ProfileChange, replaced string, err error) {
	if kind == ChangeName {
		free, err := s.nameFree(ctx, userID, value)
		if err != nil {
			return change, "", err
		}
		if !free {
			return change, "", ErrNameTaken
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return change, "", err
	}
	defer tx.Rollback(ctx)
	var old string
	err = tx.QueryRow(ctx, `DELETE FROM profile_changes WHERE user_id = $1 AND kind = $2 AND status = 'pending' RETURNING value`,
		userID, kind).Scan(&old)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return change, "", err
	}
	if kind == ChangeAvatar {
		replaced = old
	}
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO profile_changes (user_id, kind, value) VALUES ($1, $2, $3) RETURNING id`,
		userID, kind, value).Scan(&id); err != nil {
		return change, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return change, "", err
	}
	change, err = s.GetProfileChange(ctx, id)
	return change, replaced, err
}

func (s *Store) GetProfileChange(ctx context.Context, id string) (models.ProfileChange, error) {
	c, err := scanChange(s.Pool.QueryRow(ctx,
		`SELECT `+changeCols+` FROM profile_changes c JOIN users u ON u.id = c.user_id WHERE c.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// myChangesShown - сколько последних заявок игрок видит у себя в профиле.
const myChangesShown = 6

// MyProfileChanges - последние заявки игрока: ожидающие и отклонённые с причиной.
func (s *Store) MyProfileChanges(ctx context.Context, userID string) ([]models.ProfileChange, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT `+changeCols+` FROM profile_changes c JOIN users u ON u.id = c.user_id
		WHERE c.user_id = $1 ORDER BY c.created_at DESC LIMIT $2`, userID, myChangesShown)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ProfileChange{}
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PendingProfileChanges - заявки, ждущие проверки, старые сверху.
func (s *Store) PendingProfileChanges(ctx context.Context) ([]models.ProfileChange, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT `+changeCols+` FROM profile_changes c JOIN users u ON u.id = c.user_id
		WHERE c.status = 'pending' AND u.hidden_at IS NULL ORDER BY c.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ProfileChange{}
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CancelProfileChange - игрок забирает свою нерассмотренную заявку. dropped - файл фото, который больше не нужен.
func (s *Store) CancelProfileChange(ctx context.Context, id, userID string) (dropped string, err error) {
	var kind, value string
	err = s.Pool.QueryRow(ctx, `DELETE FROM profile_changes WHERE id = $1 AND user_id = $2 AND status = 'pending' RETURNING kind, value`,
		id, userID).Scan(&kind, &value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if kind == ChangeAvatar {
		dropped = value
	}
	return dropped, err
}

// DecideProfileChange одобряет заявку (ник и фото тут же меняются) или отклоняет с причиной.
// dropped - файл, который больше не нужен: отклонённое фото или прежнее фото игрока.
func (s *Store) DecideProfileChange(ctx context.Context, id string, approve bool, reason, by string) (change models.ProfileChange, dropped string, err error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return change, "", err
	}
	defer tx.Rollback(ctx)
	var userID, kind, value, status string
	err = tx.QueryRow(ctx, `SELECT user_id, kind, value, status FROM profile_changes WHERE id = $1 FOR UPDATE`, id).
		Scan(&userID, &kind, &value, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return change, "", ErrNotFound
	}
	if err != nil {
		return change, "", err
	}
	if status != "pending" {
		return change, "", ErrChangeDecided
	}
	if approve {
		switch kind {
		case ChangeName:
			var taken bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM users WHERE id <> $1 AND (lower(login) = lower($2) OR lower(display_name) = lower($2)))`,
				userID, value).Scan(&taken); err != nil {
				return change, "", err
			}
			if taken {
				return change, "", ErrNameTaken
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET display_name = $2, updated_at = now() WHERE id = $1`, userID, value); err != nil {
				return change, "", err
			}
		case ChangeAvatar:
			var prev string
			if err := tx.QueryRow(ctx, `SELECT avatar_url FROM users WHERE id = $1`, userID).Scan(&prev); err != nil {
				return change, "", err
			}
			if strings.HasPrefix(prev, mediaPrefix) {
				dropped = strings.TrimPrefix(prev, mediaPrefix)
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET avatar_url = $2, updated_at = now() WHERE id = $1`, userID, mediaPrefix+value); err != nil {
				return change, "", err
			}
		}
	} else if kind == ChangeAvatar {
		dropped = value
	}
	newStatus := "rejected"
	if approve {
		newStatus = "approved"
	}
	if _, err := tx.Exec(ctx, `UPDATE profile_changes SET status = $2, reason = $3, decided_at = now(), decided_by = $4 WHERE id = $1`,
		id, newStatus, reason, by); err != nil {
		return change, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return change, "", err
	}
	change, err = s.GetProfileChange(ctx, id)
	return change, dropped, err
}

// ClearAvatar убирает фото игрока сразу, без проверки. Возвращает файл, который можно удалить из хранилища.
func (s *Store) ClearAvatar(ctx context.Context, userID string) (string, error) {
	var prev string
	err := s.Pool.QueryRow(ctx, `
		UPDATE users u SET avatar_url = '', updated_at = now()
		FROM (SELECT avatar_url FROM users WHERE id = $1) old
		WHERE u.id = $1 RETURNING old.avatar_url`, userID).Scan(&prev)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(prev, mediaPrefix) {
		return strings.TrimPrefix(prev, mediaPrefix), nil
	}
	return "", nil
}

// SetProfileDirect сразу ставит ник или фото: у организатора и модератора проверки нет.
// dropped - прежний файл фото, который можно удалить.
func (s *Store) SetProfileDirect(ctx context.Context, userID, kind, value string) (dropped string, err error) {
	if kind == ChangeName {
		free, err := s.nameFree(ctx, userID, value)
		if err != nil {
			return "", err
		}
		if !free {
			return "", ErrNameTaken
		}
		_, err = s.Pool.Exec(ctx, `UPDATE users SET display_name = $2, updated_at = now() WHERE id = $1`, userID, value)
		return "", err
	}
	var prev string
	err = s.Pool.QueryRow(ctx, `
		UPDATE users u SET avatar_url = $2, updated_at = now()
		FROM (SELECT avatar_url FROM users WHERE id = $1) old
		WHERE u.id = $1 RETURNING old.avatar_url`, userID, mediaPrefix+value).Scan(&prev)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(prev, mediaPrefix) {
		dropped = strings.TrimPrefix(prev, mediaPrefix)
	}
	return dropped, nil
}

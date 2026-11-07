-- +goose Up
-- Задание могут засчитать и владельцу, и сопернику одновременно: отметка соперника - отдельное поле.
-- opponent_by - сторона, получившая +1 за чужое задание; completed_by по-прежнему указывает владельца,
-- если задание выполнил он, а иначе - соперника.

ALTER TABLE round_bonus_tasks ADD COLUMN IF NOT EXISTS opponent_by text;
UPDATE round_bonus_tasks SET opponent_by = completed_by
 WHERE completed_by IS NOT NULL AND completed_by <> participant_id;

-- +goose Down
ALTER TABLE round_bonus_tasks DROP COLUMN IF EXISTS opponent_by;

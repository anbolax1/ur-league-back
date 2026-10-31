-- +goose Up
-- Анкета в заявке: ранг и общая статистика игрока по его же словам, удобное время игры.
-- Статистика необязательна, поэтому числа могут быть пустыми.
ALTER TABLE registrations
    ADD COLUMN rank      text NOT NULL DEFAULT '',
    ADD COLUMN kd        numeric(6, 2),
    ADD COLUMN matches   int,
    ADD COLUMN wins      int,
    ADD COLUMN kills     int,
    ADD COLUMN play_time text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE registrations DROP COLUMN play_time, DROP COLUMN kills, DROP COLUMN wins, DROP COLUMN matches,
    DROP COLUMN kd, DROP COLUMN rank;

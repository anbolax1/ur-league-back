-- +goose Up
-- Удалённый организатором игрок: страница, рейтинг и вход закрыты, а матчи и их участники остаются в истории.
ALTER TABLE users ADD COLUMN hidden_at timestamptz;
CREATE INDEX users_hidden_idx ON users (id) WHERE hidden_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS users_hidden_idx;
ALTER TABLE users DROP COLUMN hidden_at;

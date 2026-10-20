-- +goose Up
-- Вместо Embark ID - игровой ID PUBG Mobile (одни цифры) и ник в игре. Один ID - один игрок,
-- а ID в формате Embark («ник#1234») для PUBG не годится и стирается.
ALTER TABLE users RENAME COLUMN embark_id TO game_id;
ALTER TABLE users ADD COLUMN IF NOT EXISTS game_nick text NOT NULL DEFAULT '';
UPDATE users SET game_id = '' WHERE game_id !~ '^[0-9]{6,12}$';
CREATE UNIQUE INDEX IF NOT EXISTS users_game_id_key ON users (game_id) WHERE game_id <> '';

ALTER TABLE registrations RENAME COLUMN embark_id TO game_id;
ALTER TABLE registrations ADD COLUMN IF NOT EXISTS game_nick text NOT NULL DEFAULT '';
UPDATE registrations SET game_id = '' WHERE game_id !~ '^[0-9]{6,12}$';

-- +goose Down
ALTER TABLE registrations DROP COLUMN IF EXISTS game_nick;
ALTER TABLE registrations RENAME COLUMN game_id TO embark_id;
DROP INDEX IF EXISTS users_game_id_key;
ALTER TABLE users DROP COLUMN IF EXISTS game_nick;
ALTER TABLE users RENAME COLUMN game_id TO embark_id;

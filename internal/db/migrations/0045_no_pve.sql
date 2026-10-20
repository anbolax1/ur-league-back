-- +goose Up
-- В Ultimate Royale нет PvE: у матча нет типа игроков, у заданий и легендарок - вида, пул заданий один.
ALTER TABLE tournaments DROP COLUMN IF EXISTS player_type;
ALTER TABLE catalog_tasks DROP COLUMN IF EXISTS kind;
ALTER TABLE legendary_contracts DROP COLUMN IF EXISTS kind;

-- +goose Down
ALTER TABLE legendary_contracts ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'pvpve';
ALTER TABLE catalog_tasks ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'pvpve';
ALTER TABLE tournaments ADD COLUMN IF NOT EXISTS player_type text NOT NULL DEFAULT 'pvpve';

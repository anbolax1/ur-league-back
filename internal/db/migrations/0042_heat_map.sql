-- +goose Up
-- Карту в Ultimate Royale выбирает игра, поэтому пиков-банов нет: у каждого захода стороны своя карта,
-- её отмечает ведущий. Задание на карту выдаётся по этой отметке, и при смене карты меняется только оно.
DROP TABLE IF EXISTS match_veto;
ALTER TABLE round_entries ADD COLUMN IF NOT EXISTS map_code text REFERENCES maps(code) ON DELETE SET NULL;
ALTER TABLE round_bonus_tasks ADD COLUMN IF NOT EXISTS map_slot boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE round_bonus_tasks DROP COLUMN IF EXISTS map_slot;
ALTER TABLE round_entries DROP COLUMN IF EXISTS map_code;
CREATE TABLE IF NOT EXISTS match_veto (
    id            text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    tournament_id text NOT NULL REFERENCES tournaments(id) ON DELETE CASCADE,
    seq           int  NOT NULL,
    action        text NOT NULL,
    side          text NOT NULL DEFAULT '',
    map_code      text NOT NULL REFERENCES maps(code),
    round_number  int,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tournament_id, seq),
    UNIQUE (tournament_id, map_code)
);

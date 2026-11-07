-- +goose Up
-- Ручная передача пояса лиги организатором: носитель ушёл из игры, пояс отдают без матча.
-- Пояс по-прежнему выводится из истории; передача - такое же событие, как матч.

CREATE TABLE IF NOT EXISTS belt_transfers (
    id         text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    from_user  text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    to_user    text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    by_user    text REFERENCES users(id) ON DELETE SET NULL,
    season_id  text REFERENCES seasons(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_belt_transfers_created ON belt_transfers(created_at);

-- +goose Down
DROP TABLE IF EXISTS belt_transfers;

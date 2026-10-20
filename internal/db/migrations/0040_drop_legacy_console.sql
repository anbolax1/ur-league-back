-- +goose Up
-- Старый пульт (один раунд, основные задания, протоколы-штрафы 2 сезона) лиге не нужен: его таблицы
-- уходят вместе с ним, у результата раунда остаются очки и ноки.
DROP TABLE IF EXISTS round_starter_task_done;
DROP TABLE IF EXISTS round_starter_tasks;
DROP TABLE IF EXISTS starter_tasks;
DROP TABLE IF EXISTS round_penalties;
DROP TABLE IF EXISTS catalog_complications;
ALTER TABLE round_entries DROP COLUMN IF EXISTS tasks;
ALTER TABLE round_entries DROP COLUMN IF EXISTS bonus;
ALTER TABLE round_entries DROP COLUMN IF EXISTS complications;

-- +goose Down
-- Таблицы возвращаются пустыми: задания и протоколы старого пульта не восстановить.
ALTER TABLE round_entries ADD COLUMN IF NOT EXISTS tasks jsonb NOT NULL DEFAULT '[]';
ALTER TABLE round_entries ADD COLUMN IF NOT EXISTS bonus jsonb NOT NULL DEFAULT '[]';
ALTER TABLE round_entries ADD COLUMN IF NOT EXISTS complications jsonb NOT NULL DEFAULT '[]';

CREATE TABLE IF NOT EXISTS catalog_complications (
    id         text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    text       text NOT NULL,
    penalty    int  NOT NULL DEFAULT 1,
    value_type text NOT NULL DEFAULT 'fixed',
    source     text NOT NULL DEFAULT 'official',
    author     text NOT NULL DEFAULT '',
    title      text NOT NULL DEFAULT '',
    sort_order int  NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS round_penalties (
    id              text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    round_id        text NOT NULL REFERENCES rounds(id) ON DELETE CASCADE,
    participant_id  text NOT NULL REFERENCES participants(id) ON DELETE CASCADE,
    complication_id text NOT NULL REFERENCES catalog_complications(id) ON DELETE CASCADE,
    times           int  NOT NULL DEFAULT 0,
    UNIQUE(round_id, participant_id, complication_id)
);
CREATE INDEX IF NOT EXISTS idx_rp_round ON round_penalties(round_id);
CREATE INDEX IF NOT EXISTS idx_rp_participant ON round_penalties(participant_id);

CREATE TABLE IF NOT EXISTS starter_tasks (
    id         text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    text       text NOT NULL,
    points     int  NOT NULL DEFAULT 0,
    sort_order int  NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    kind       text NOT NULL DEFAULT 'pvpve'
);

CREATE TABLE IF NOT EXISTS round_starter_tasks (
    id              text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    round_id        text NOT NULL REFERENCES rounds(id) ON DELETE CASCADE,
    starter_task_id text NOT NULL REFERENCES starter_tasks(id) ON DELETE CASCADE,
    completed_by    text REFERENCES participants(id) ON DELETE SET NULL,
    times           int  NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE(round_id, starter_task_id)
);
CREATE INDEX IF NOT EXISTS idx_rst_round ON round_starter_tasks(round_id);

CREATE TABLE IF NOT EXISTS round_starter_task_done (
    id                    text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    round_starter_task_id text NOT NULL REFERENCES round_starter_tasks(id) ON DELETE CASCADE,
    participant_id        text NOT NULL REFERENCES participants(id) ON DELETE CASCADE,
    times                 int  NOT NULL DEFAULT 0,
    UNIQUE(round_starter_task_id, participant_id)
);
CREATE INDEX IF NOT EXISTS idx_rstd_participant ON round_starter_task_done(participant_id);

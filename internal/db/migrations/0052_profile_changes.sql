-- +goose Up
-- Заявки игроков на смену фото и ника: на сайт попадают после одобрения организатором или модератором.
-- Value - новый ник либо путь картинки в хранилище медиа.
CREATE TABLE profile_changes (
    id         text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id    text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('avatar', 'name')),
    value      text NOT NULL,
    status     text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    reason     text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    decided_at timestamptz,
    decided_by text REFERENCES users(id) ON DELETE SET NULL
);
-- Новая заявка того же вида заменяет прежнюю нерассмотренную.
CREATE UNIQUE INDEX profile_changes_pending ON profile_changes (user_id, kind) WHERE status = 'pending';
CREATE INDEX profile_changes_user ON profile_changes (user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS profile_changes;

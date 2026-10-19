-- +goose Up
-- Дизайн у лиги один: переключать нечего, общие настройки сайта больше не хранятся.
DROP TABLE IF EXISTS site_settings;

-- +goose Down
CREATE TABLE IF NOT EXISTS site_settings (
    key        text PRIMARY KEY,
    value      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO site_settings (key, value) VALUES ('design', 'surface') ON CONFLICT (key) DO NOTHING;

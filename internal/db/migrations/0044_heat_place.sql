-- +goose Up
-- Место стороны в матче PUBG по экрану итогов захода: 1-16 (в Ultimate Royale 16 команд). Очков не даёт,
-- нужно для статистики и «чикенов» - первых мест.
ALTER TABLE round_entries ADD COLUMN IF NOT EXISTS place int CHECK (place BETWEEN 1 AND 16);

-- +goose Down
ALTER TABLE round_entries DROP COLUMN IF EXISTS place;

-- +goose Up
-- Составы лиги: ведущий заходит в группу вместе с игроками стороны, а мест в группе четыре -
-- поэтому в команде два (2×2) или три (3×3) игрока. Ключ тройки - три отсортированных userId через «|».
ALTER TABLE team_mmr ADD COLUMN IF NOT EXISTS member_c text REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE team_mmr ADD COLUMN IF NOT EXISTS mode text NOT NULL DEFAULT '2x2';
ALTER TABLE team_mmr ADD CONSTRAINT team_mmr_mode_check
    CHECK ((mode = '2x2' AND member_c IS NULL) OR (mode = '3x3' AND member_c IS NOT NULL));

-- +goose Down
DELETE FROM team_mmr_history WHERE team_key IN (SELECT team_key FROM team_mmr WHERE mode = '3x3');
DELETE FROM team_mmr WHERE mode = '3x3';
ALTER TABLE team_mmr DROP CONSTRAINT IF EXISTS team_mmr_mode_check;
ALTER TABLE team_mmr DROP COLUMN IF EXISTS mode;
ALTER TABLE team_mmr DROP COLUMN IF EXISTS member_c;

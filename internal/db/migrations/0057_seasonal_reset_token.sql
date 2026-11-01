-- +goose Up
-- Жетон сброса рейтинга выдаётся каждому игроку на каждый сезон сам, при первом обращении к жетонам в сезоне
-- (store.ensureSeasonalReset). Индекс не даёт выдать его дважды за один сезон.
CREATE UNIQUE INDEX IF NOT EXISTS token_ledger_season_reset ON token_ledger (user_id, season_id)
    WHERE kind = 'reset' AND action = 'grant' AND note = 'жетон сезона';

-- Жетоны, выданные всем при запуске, считаются жетонами идущего сезона.
UPDATE token_ledger SET note = 'жетон сезона', season_id = (SELECT id FROM seasons WHERE status = 'active' LIMIT 1)
WHERE kind = 'reset' AND action = 'grant' AND note = 'выдано всем игрокам при запуске жетонов'
  AND EXISTS (SELECT 1 FROM seasons WHERE status = 'active');

-- +goose Down
DROP INDEX IF EXISTS token_ledger_season_reset;

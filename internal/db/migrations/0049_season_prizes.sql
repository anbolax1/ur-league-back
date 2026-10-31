-- +goose Up
-- Призовой фонд сезона: общая сумма и суммы за места по порядку (первое - первым). Пустой список - призов нет.
ALTER TABLE seasons
    ADD COLUMN prize_pool   int   NOT NULL DEFAULT 0,
    ADD COLUMN prize_places int[] NOT NULL DEFAULT '{}',
    ADD COLUMN prize_note   text  NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE seasons DROP COLUMN prize_note, DROP COLUMN prize_places, DROP COLUMN prize_pool;

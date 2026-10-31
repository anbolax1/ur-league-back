-- +goose Up
-- Каждому игроку на сайте - по одному жетону сброса рейтинга. Миграция выполняется один раз:
-- кто зарегистрируется позже, жетон получит от модератора или организатора.
INSERT INTO token_ledger (user_id, kind, action, delta, note)
SELECT id, 'reset', 'grant', 1, 'выдано всем игрокам при запуске жетонов'
FROM users
WHERE hidden_at IS NULL;

-- +goose Down
DELETE FROM token_ledger WHERE kind = 'reset' AND action = 'grant' AND note = 'выдано всем игрокам при запуске жетонов';

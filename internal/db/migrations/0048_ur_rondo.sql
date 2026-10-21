-- +goose Up
-- Третья карта Ultimate Royale - Рондо. Картинка - официальный скриншот PUBG из Steam, как у Эрангеля и Мирамара.
INSERT INTO maps (code, name, image, sort_order) VALUES
    ('rondo', 'Рондо', '/maps/rondo.jpg', 3)
ON CONFLICT (code) DO NOTHING;

-- +goose Down
DELETE FROM maps WHERE code = 'rondo';

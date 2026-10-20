-- +goose Up
-- Ultimate Royale играют на Эрангеле и Мирамаре: карты ARC Raiders уходят из справочника. Картинки -
-- официальные скриншоты PUBG из Steam.
DELETE FROM maps WHERE code IN ('stormy_flows', 'blue_gate', 'buried_city', 'spaceport', 'dam', 'stella_montis', 'pendola_pass');
INSERT INTO maps (code, name, image, sort_order) VALUES
    ('erangel', 'Эрангель', '/maps/erangel.jpg', 1),
    ('miramar', 'Мирамар',  '/maps/miramar.jpg', 2)
ON CONFLICT (code) DO NOTHING;

-- +goose Down
DELETE FROM maps WHERE code IN ('erangel', 'miramar');
INSERT INTO maps (code, name, image, sort_order, added_at) VALUES
    ('stormy_flows',  'Бурные потоки',      '/maps/stormy_flows.jpg',  1, 'epoch'),
    ('blue_gate',     'Синие ворота',       '/maps/blue_gate.jpg',     2, 'epoch'),
    ('buried_city',   'Погребённый город',  '/maps/buried_city.jpg',   3, 'epoch'),
    ('spaceport',     'Космопорт',          '/maps/spaceport.jpg',     4, 'epoch'),
    ('dam',           'Поле битвы у Дамбы', '/maps/dam.jpg',           5, 'epoch'),
    ('stella_montis', 'Стелла Монтис',      '/maps/stella_montis.jpg', 6, 'epoch'),
    ('pendola_pass',  'Перевал Пендола',    '/maps/pendola_pass.jpg',  7, now())
ON CONFLICT (code) DO NOTHING;

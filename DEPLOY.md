# Деплой бэкенда (обновление)

Прод: `brouhub.ru/api` → Go API лиги на VPS `155.212.133.147`, порт `8090`, под пользователем
`admin2` (systemd --user сервис `ur-back`). Postgres — системный, локально на сервере, база `ur_league`.

> Сервер под лигу ещё не настроен: первый раз — шаги из `deploy/DEPLOY.md` в корне `UR_League`.
> На том же сервере работает GOARC (`respect-back`, порт 8080) — его не трогаем.

**Принцип:** собираем бинарь **локально**, на сервер кладём только его. Исходники на сервер
не заливаем. Миграции БД накатываются автоматически бинарём при старте.

> Команды — для Git Bash на Windows. Доступ к серверу — по SSH как `admin2`
> (если ключ не настроен — см. «Если нет SSH-доступа как admin2» внизу).

---

## Шаги

```bash
cd backend
VPS=155.212.133.147

# 1. Кросс-сборка статического бинаря под Linux
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o server-linux ./cmd/server

# 2. Залить рядом со старым (не поверх работающего — иначе "text file busy")
scp server-linux admin2@$VPS:~/ur-back/server-linux.new

# 3. Подменить и перезапустить (миграции прогонятся на старте)
ssh admin2@$VPS '
  chmod +x ~/ur-back/server-linux.new
  mv -f ~/ur-back/server-linux.new ~/ur-back/server-linux
  systemctl --user restart ur-back
'
```

## Проверка

```bash
# health
curl -s https://brouhub.ru/api/health        # {"ok":true,...}

# что миграции накатились (смотрим лог сервиса; sudo не нужен)
ssh admin2@$VPS 'journalctl --user -u ur-back -n 20 --no-pager | grep -iE "goose|migrat|version"'
# ждём строку вида: "successfully migrated database to version: N"
```

## Миграции БД

- Файлы — `internal/db/migrations/000N_*.sql` (goose, встроены в бинарь).
- **Накатываются сами** при старте бэка — отдельной команды не нужно.
- Новая миграция = новый файл с бóльшим номером; модели/сторы под неё правим в Go-коде.
- Откат миграции автоматом **не** делается. Если нужно — `-- +goose Down` секция есть в каждом
  файле, но на проде откатывать вручную через psql и только осознанно (бэкап сначала).
- Версию БД на проде видно в логе старта (см. «Проверка») либо через psql по `DATABASE_URL` из `~/ur-back/.env`.

## Откат (rollback) бинаря

Бинарь не версионируется на сервере. Чтобы быстро откатиться — перед заливкой сохрани текущий:
```bash
ssh admin2@$VPS 'cp ~/ur-back/server-linux ~/ur-back/server-linux.bak'   # ПЕРЕД шагом 3
# откат:
ssh admin2@$VPS 'mv -f ~/ur-back/server-linux.bak ~/ur-back/server-linux && systemctl --user restart ur-back'
```
> ⚠️ Откат бинаря **не откатывает миграции**. Если новая версия накатила миграцию, старый
> бинарь должен уметь работать с новой схемой (обычно да — схема расширяется совместимо).

## Конфиг / секреты

- Лежат в `~/ur-back/.env` на сервере (скрытый файл, виден через `ls -a`): `JWT_SECRET`
  (случайный ≥32 символов), `DATABASE_URL`, `SUPERADMIN_LOGIN`/`SUPERADMIN_PASSWORD` (бутстрап
  организатора), `COOKIE_*`, `TRUST_PROXY=true` (за nginx) и `BIND_ADDR=127.0.0.1:8090` и т.д.
- В репозитории — только шаблон `.env.example`. Реальный `.env` в git не попадает (gitignore).
- Меняешь `.env` → `systemctl --user restart ur-back`.


## Траблшутинг

```bash
ssh admin2@$VPS 'systemctl --user status ur-back --no-pager'      # состояние
ssh admin2@$VPS 'journalctl --user -u ur-back -n 50 --no-pager'   # последние логи
ssh admin2@$VPS 'systemctl --user restart ur-back'                # перезапуск
```
- 502 на `brouhub.ru/api` → бэк упал, смотри логи (часто — БД недоступна или паника в миграции).
- Сервис не стартует после перезагрузки сервера → проверь linger: `loginctl show-user admin2 | grep Linger` (должно `Linger=yes`).

## Если нет SSH-доступа как admin2 (с этой машины)

Первый деплой делали через PuTTY под root (у admin2 вход по ключу). Аналог `scp`/`ssh`:
```bash
PSCP="/c/Program Files/PuTTY/pscp.exe"; PLINK="/c/Program Files/PuTTY/plink.exe"
HK="SHA256:QiiRhzn951tFxzGizQDfEYI8SfXyqNIBfOi3pmkCgAM"   # отпечаток хоста
"$PSCP" -hostkey "$HK" -pw '<root-пароль>' server-linux root@155.212.133.147:/home/admin2/ur-back/server-linux.new
"$PLINK" -batch -hostkey "$HK" -pw '<root-пароль>' root@155.212.133.147 '
  chown admin2:admin2 /home/admin2/ur-back/server-linux.new
  chmod +x /home/admin2/ur-back/server-linux.new
  mv -f /home/admin2/ur-back/server-linux.new /home/admin2/ur-back/server-linux
  systemctl --user -M admin2@ restart ur-back
'
```
> Под root перезапуск user-сервиса — через `systemctl --user -M admin2@ ...`.

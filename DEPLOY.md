# Развёртывание в продакшен (otklik)

Краткий гайд: поднять стенд с нуля, включить все защитные механизмы и
проверить, что они работают. Порядок шагов важен — секреты, сгенерированные
на шаге 1, используются дальше по тексту.

Все команды выполняются на сервере (Linux + Docker + docker compose plugin)
в корне склонированного репозитория.

## 0. Требования к серверу

- Docker Engine ≥ 24 и docker compose (plugin) — единственные зависимости,
  приложение и БД живут в контейнерах.
- Открыты порты 80, 443 (публичный сайт) и 8443 (панель сотрудников).
- Домен (или два: `example.ru` и `staff.example.ru`), A-записи указывают на
  сервер. Для голого IP Caddy выпустит самоподписанный сертификат (браузер
  будет предупреждать) — для прода нужен домен: Web Push и куки `Secure`
  рассчитаны на нормальный HTTPS.

## 1. Секреты (.env)

```bash
cp .env.example .env
```

Заполнить обязательно (значения сгенерировать на сервере и сохранить в
менеджере секретов; при утере BACKUP_KEY бэкапы не восстановить):

```bash
POSTGRES_PASSWORD=$(openssl rand -hex 16)      # пароль БД
SEED_DEFAULT_PWD=$(openssl rand -hex 12)       # демо-пароль сотрудников
BACKUP_KEY=$(openssl rand -hex 32)             # ключ шифрования бэкапов
TRACK_HMAC_KEY=$(openssl rand -hex 32)         # соль HMAC трек-номеров
```

Остальное:

- `COOKIE_SECURE=1` — обязательно на проде (куки только по HTTPS).
- `PUBLIC_SITE` / `STAFF_SITE` — домены, см. комментарий в `.env.example`.
- `RETENTION_DAYS` — сколько дней хранить завершённые обращения с персональными
  данными детей (по умолчанию 730 = 2 года; 0 — бессрочно).
- `TOTP_ENABLED=1` — двухфакторная аутентификация для сотрудников
  (включается в /profile; для админа настоятельно рекомендуется).

**TRACK_HMAC_KEY и VAPID-ключи после первого запуска не менять** (см. ниже) —
смена приводит к потере входа по старым трек-номерам / отзыву push-подписок.

## 2. Web Push (уведомления при закрытой вкладке)

Без этого шага всё работает, но уведомления — только в открытой вкладке.

```bash
# на машине с Go (или docker run --rm -v "$PWD:/src" -w /src golang:1.24-alpine go run ./cmd/vapidkeygen)
go run ./cmd/vapidkeygen
```

Вывод скопировать в `.env` (`VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY`), там же
указать `PUSH_SUBJECT=mailto:admin@ваш-домен`. Ключи генерируются один раз
навсегда: смена пары отзывает подписки всех браузеров (заявители переподпишутся
при следующем визите, но уведомления об автозакрытии пропадут).

Push требует HTTPS-домена (шаг 0) — service worker и push API работают только
в secure context.

## 3. Сборка и запуск

```bash
OTKLIK_VERSION=$(git rev-parse --short HEAD) docker compose build app
docker compose up -d
docker compose ps          # все контейнеры healthy
```

Версия сборки видна в `GET /api/health` (`version`) и в версии кэша
service worker — собирайте всегда с `OTKLIK_VERSION`, иначе после релизов
у части пользователей останется старый кэш PWA.

### 3.1. Общий сервер: host-nginx вместо встроенного Caddy

Если на сервере уже работают другие проекты и порты 80/443 держит хостовый
nginx (не docker) — встроенный `caddy` не нужен: он по умолчанию НЕ стартует
(профиль `edge`). TLS терминирует ваш nginx, otklik сидит на 127.0.0.1.

Конфликты портов решаются в `.env` (порты заняты другими проектами):

```bash
OTKLIK_PUBLIC_PORT=8090     # 8080 часто занят чужим бэкендом
OTKLIK_STAFF_PORT=8091      # 8081 часто занят чужим фронтендом
OTKLIK_DB_PORT=5434         # 5433 может быть занят чужим postgres
```

Дальше два vhost'а в nginx (`/etc/nginx/sites-available/`, симлинки в
`sites-enabled/`; сертификаты — certbot, как для остальных ваших сайтов):

```nginx
# ---- заявители: PUBLIC_SITE -> 127.0.0.1:8090 ----
server {
    listen 80;
    server_name otklik.example.com;
    return 301 https://$host$request_uri;
}
server {
    listen 443 ssl http2;
    server_name otklik.example.com;
    # ssl_certificate / ssl_certificate_key — certbot, как у ваших сайтов

    # вложения до 10 МБ — дефолт nginx (1 МБ) режет загрузку
    client_max_body_size 12m;

    location / {
        proxy_pass http://127.0.0.1:8090;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}

# ---- сотрудники: STAFF_SITE -> 127.0.0.1:8091 ----
server {
    listen 80;
    server_name staff.otklik.example.com;
    return 301 https://$host$request_uri;
}
server {
    listen 443 ssl http2;
    server_name staff.otklik.example.com;

    client_max_body_size 12m;

    # /metrics наружу закрыт (Prometheus-эндпоинт без авторизации);
    # скрейпер ходит с сервера: curl 127.0.0.1:8091/metrics
    location /metrics { return 404; }

    location / {
        proxy_pass http://127.0.0.1:8091;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Почему это безопасно:

- приложение слушает только `127.0.0.1:8090/8091` — снаружи напрямую не достучаться;
- рейт-лимиты считают адрес из `X-Forwarded-For` только от доверенных
  (loopback/приватных) прокси — nginx как раз такой, подделка XFF клиентом
  не работает, лимиты считают реальные адреса;
- `Host` обязателен (CSRF-проверки same-origin), `X-Forwarded-Proto` — чтобы
  приложение видело схему за прокси.

Проверка: `nginx -t && systemctl reload nginx`, затем чек-лист раздела 8.
Выключенный caddy можно в любой момент включить (одиночный сервер):
`docker compose --profile edge up -d` — но тогда 80/443 должны быть свободны.

## 4. Первый вход и учётные записи

При первом старте создаются демо-аккаунты (`admin`, `operator`,
`psychologist1`, ...) с паролем из `SEED_DEFAULT_PWD`. Каждый обязан сменить
пароль при первом входе (API закрыт до смены — фронт сам покажет диалог).

- Панель сотрудников: `https://STAFF_SITE/`
- Немедленно: войти под `admin`, сменить пароль, включить 2FA в `/profile`,
  удалить/деактивировать неиспользуемые демо-аккаунты (админка → пользователи).
- Если `SEED_DEFAULT_PWD` остался пустым — пароль печатается в лог один раз:
  `docker compose logs app | grep 'random demo password'`.

## 5. Бэкапы: локальные, off-site, проверка восстановления

Локальные бэкапы делает сервис `db-backup` автоматически (раз в сутки,
шифруются `BACKUP_KEY`, хранятся `BACKUP_KEEP` копий). Проверка после
развёртывания:

```bash
docker compose exec db-backup ls -lh /backups   # есть свежий .enc
```

**Off-site копия** (обязательно: бэкап на том же диске не спасает от потери
диска):

```bash
# 1) настроить удалённое хранилище (S3/Яндекс.Диск/SFTP/...)
cat > rclone.conf <<EOF
[offsite]
type = s3
# ... параметры вашего хранилища
EOF
# 2) в .env: BACKUP_OFFSITE_REMOTE=offsite:otklik-backups
# 3) включить:
docker compose --profile offsite up -d
```

**Проверка восстановления** (делать сразу после развёртывания и далее
периодически):

```bash
docker compose exec -T db psql -U otklik -d postgres -c "DROP DATABASE IF EXISTS otklik_verify;"
docker compose exec -T db psql -U otklik -d postgres -c "CREATE DATABASE otklik_verify;"
docker compose exec db-backup sh -c \
  'openssl enc -d -aes-256-cbc -pbkdf2 -in "$(ls -1t /backups/otklik-*.dump.enc | head -1)" -pass env:BACKUP_KEY \
   | pg_restore -U otklik -d otklik_verify --no-owner'
docker compose exec -T db psql -U otklik -d otklik_verify -c "SELECT count(*) FROM appeals;"
docker compose exec -T db psql -U otklik -d postgres -c "DROP DATABASE otklik_verify;"
```

Восстановление в рабочую базу — `pg_restore -U otklik -d otklik --clean` вместо
`otklik_verify` (останавливать приложение не обязательно, но новые обращения
за время восстановления пропадут).

## 6. Мониторинг

- `GET /api/health` — живость (оба порта); уже в healthcheck контейнеров.
- `GET /metrics` — Prometheus-эндпоинт (версия, счётчики HTTP, память,
  горутины). Снаружи закрыт в Caddy; скрейпер ходит с самого сервера:
  `curl http://127.0.0.1:8081/metrics` (порт опубликован только на localhost).
- Freshness-хелсчеки бэкапов: `docker compose ps` показывает `unhealthy` у
  `db-backup`, если свежий бэкап старше 25 ч, и у `backup-offsite`, если
  копия в облако не обновлялась 25 ч. Алертить на состояние контейнеров.
- Логи — JSON в stdout (`LOG_FORMAT=json`): `docker compose logs -f app`.
  Ротация настроена в compose (10 МБ × 3 на сервис).

## 7. Обновление релиза

Всё делает `./deploy.sh` (пул, проверки, сборка, рестарт, контроль живости):

```bash
./deploy.sh          # задеплоит, если есть новые коммиты; повторный запуск безопасен
./deploy.sh --force  # передеплоить текущий коммит принудительно
```

Скрипт: проверяет окружение и секреты, делает ff-only pull, валидирует
compose-файл и собирает образ **до** остановки работающих контейнеров,
после `up -d` ждёт healthcheck и сверяет версию в `/api/health` с собранным
git-хешем (совпадение = миграции прошли и сервер слушает). При проблемах
печатает логи и готовые команды отката.

Вручную (эквивалент):

```bash
git pull
OTKLIK_VERSION=$(git rev-parse --short HEAD) docker compose build app
docker compose up -d app          # миграции накатываются при старте
docker compose logs app | tail    # убедиться: listening, migrations applied
```

Миграции применяются автоматически и идемпотентны; откат версии приложения
после применённой миграции не поддерживается (как обычно).

## 8. Чек-лист перед открытием доступа

- [ ] `.env`: POSTGRES_PASSWORD, SEED_DEFAULT_PWD, BACKUP_KEY, TRACK_HMAC_KEY заданы (не `change_me`)
- [ ] `COOKIE_SECURE=1`
- [ ] `TOTP_ENABLED=1`, у админа включена 2FA, пароль сменён
- [ ] Домены отвечают по HTTPS, сертификат Let's Encrypt выпущен (не self-signed)
- [ ] `GET /api/health` → `{"status":"ok","version":"<git-hash>"}`
- [ ] `https://<staff-домен>/metrics` → 404 (закрыт снаружи), `curl 127.0.0.1:8081/metrics` работает
- [ ] VAPID-ключи в `.env`, тестовое обращение: подписка на уведомления проходит
- [ ] `db-backup` healthy, свежий `.enc` в `/backups`
- [ ] Восстановление из бэкапа проверено (шаг 5)
- [ ] `backup-offsite` запущен (`--profile offsite`) и healthy
- [ ] Неиспользуемые демо-аккаунты удалены/деактивированы
- [ ] Словарь кризисных маркеров просмотрен админом (`/admin` → раздел маркеров)

## 9. Известные ограничения

- Приложение однопроцессное (один контейнер `app`); горизонтальное
  масштабирование не предусматривалось — при росте нагрузки сначала
  увеличивать ресурсы контейнера.
- `TRACK_HMAC_KEY` нельзя менять после включения; VAPID-пару — после первой
  подписки. Если ключи всё же сменить, ничего не ломается навсегда: старые
  обращения с ключом потеряют вход по трек-номеру, подписки переподпишутся
  при следующем визите — но часть пользователей это затронет.
- SSE/WebSocket нет: чат обновляется поллингом (30 с в фоне, мгновенно при
  возврате на вкладку) + Web Push — этого достаточно и не требует
  специальной настройки прокси.


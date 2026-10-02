#!/bin/sh
# Деплой на сервере: забрать всё из git, заранее проверить, перезапустить
# обновлённую версию и убедиться, что она жива. Запуск из корня репозитория:
#   ./deploy.sh           # задеплоить, если есть что нового (безопасный повтор)
#   ./deploy.sh --force   # передеплоить, даже если новых коммитов нет
#
# Что делает по шагам:
#   1) проверяет окружение (docker, compose, .env с секретами);
#   2) git fetch + ff-only pull (никаких локальных коммитов поверх);
#   3) валидирует docker-compose.yml ДО касания работающих контейнеров;
#   4) собирает новый образ (ошибка компиляции = рестарта не будет);
#   5) docker compose up -d (актуализирует и compose-изменения: caddy и т.п.);
#   6) ждёт healthcheck контейнера app и сверяет версию в /api/health
#      с собранным git-хешем (это значит: миграции прошли, сервер слушает);
#   7) при проблемах печатает логи и точные команды отката.
set -eu

cd "$(dirname "$0")"

REMOTE="${GIT_REMOTE:-origin}"   # имя remote (на сервере обычно origin)
BRANCH=main
FORCE=0
[ "${1:-}" = "--force" ] && FORCE=1

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWARN:\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31mFAIL:\033[0m %s\n' "$*" >&2; exit 1; }

# --- 1. Окружение -----------------------------------------------------------
command -v git >/dev/null         || die "git не найден"
docker compose version >/dev/null 2>&1 || die "docker compose не найден"
[ -f .env ] || die "нет .env (cp .env.example .env, см. DEPLOY.md шаг 1)"

# Секреты, без которых стек просто не поднимется — проверяем до пулла,
# чтобы не оставить репозиторий на новом коммите с неработающим деплоем.
env_val() { grep -E "^$1=" .env | head -n1 | cut -d= -f2-; }
[ -n "$(env_val POSTGRES_PASSWORD)" ] && [ "$(env_val POSTGRES_PASSWORD)" != "default_password" ] \
	|| die "POSTGRES_PASSWORD не задан (или дефолтный) в .env"
[ -n "$(env_val BACKUP_KEY)" ] && [ "$(env_val BACKUP_KEY)" != "change_me_long_random_string" ] \
	|| die "BACKUP_KEY не задан (или дефолтный) в .env"
[ -n "$(env_val TRACK_HMAC_KEY)" ] || warn "TRACK_HMAC_KEY пуст — трек-хеши несолёные (DEPLOY.md шаг 1)"
grep -Eq '^COOKIE_SECURE=(1|true)' .env || warn "COOKIE_SECURE!=1 — на проде обязательно!"

# --- 2. Забираем всё из git -------------------------------------------------
log "git: fetch $REMOTE/$BRANCH"
git fetch "$REMOTE" "$BRANCH" || die "git fetch не удался (сеть?)"

OLD_SHA=$(git rev-parse --short HEAD)
NEW_SHA=$(git rev-parse --short "$REMOTE/$BRANCH")

if [ "$FORCE" -eq 0 ] && [ "$OLD_SHA" = "$NEW_SHA" ]; then
	log "Новых коммитов нет ($OLD_SHA), обновление не требуется. Запуск с --force принудительный."
	exit 0
fi

log "git: $OLD_SHA -> $NEW_SHA (ff-only)"
git merge --ff-only "FETCH_HEAD" || die "ff-only не удался: на сервере локальные коммиты? разберитесь вручную"
git log --oneline "$OLD_SHA..$NEW_SHA" 2>/dev/null || true

# --- 3. Предварительная проверка: compose-файл валиден ----------------------
log "проверка: docker compose config"
docker compose config --quiet || die "docker-compose.yml невалиден — работающие контейнеры не тронуты"

# --- 4. Сборка (ошибка компиляции не трогает работающий стек) ---------------
log "сборка образа app (версия $NEW_SHA)"
OTKLIK_VERSION="$NEW_SHA" docker compose build app \
	|| die "сборка не удалась — работающие контейнеры не тронуты"

# --- 5. Перезапуск ----------------------------------------------------------
log "docker compose up -d (обновятся app и изменения compose: caddy, бэкапы...)"
docker compose up -d

# --- 6. Ждём здоровья и сверяем версию --------------------------------------
log "ждём healthcheck контейнера app (до 90 с)..."
i=0
while [ $i -lt 30 ]; do
	STATE=$(docker inspect -f '{{.State.Health.Status}}' otklik-app 2>/dev/null || echo unknown)
	[ "$STATE" = "healthy" ] && break
	i=$((i + 1)); sleep 3
done
[ "$STATE" = "healthy" ] || {
	docker compose logs --tail 50 app || true
	die "контейнер app не стал healthy за 90 с (логи выше)"
}

log "сверяем версию в /api/health с собранной ($NEW_SHA)"
i=0
HEALTH=""
while [ $i -lt 10 ]; do
	HEALTH=$(docker compose exec -T app wget -q -O- http://127.0.0.1:8080/api/health 2>/dev/null || true)
	echo "$HEALTH" | grep -q "\"version\":\"$NEW_SHA\"" && break
	i=$((i + 1)); sleep 3
done
echo "$HEALTH" | grep -q "\"version\":\"$NEW_SHA\"" || {
	docker compose logs --tail 50 app || true
	die "/api/health не показывает версию $NEW_SHA: $HEALTH"
}

# --- 7. Готово --------------------------------------------------------------
log "деплой $OLD_SHA -> $NEW_SHA прошёл: приложение живо, версия совпадает."
docker compose ps
echo
log "последние события приложения:"
docker compose logs --tail 20 app || true
echo
echo "Откат (если что-то пойдёт не так позже):"
echo "  git checkout $OLD_SHA"
echo "  OTKLIK_VERSION=$OLD_SHA docker compose build app && docker compose up -d app"
echo "  git checkout $BRANCH   # вернуть ветку после разбора"

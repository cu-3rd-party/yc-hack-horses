# PR: инфраструктура V0, CI и деплой центральной ноды

Ветка `feat/dev-infrastructure`. Зона: `infra/` (кроме `infra/scripts/gen-dev-certs.sh`),
`.forgejo/workflows/ci.yml`, Makefile, README-разделы по запуску. `backend/`, `app/`,
`desktop/`, `packages/`, `docs/` не тронуты.

## Что внутри

- `infra/compose/docker-compose.yml` — центральная нода: postgres 18 (volume,
  healthcheck) + нода, порт 8080, env из `.env` (шаблон `.env.example`), mTLS-сертификаты
  read-only в `/run/secrets` (не пекутся в образ; генерирует `make dev-certs`,
  до запуска скрипта каталог пуст — нода поднимается и без mTLS).
  Проверено локально: `compose up` — postgres healthy, нода отвечает `/health` 200.
- `infra/deploy/Dockerfile.draft` + `nginx-draft.conf` — черновой образ центральной
  ноды для CI, пока `backend/Dockerfile` не готов (отвечает 200 на `/health`).
- `.ci/changes.py` + `.forgejo/workflows/ci.yml` — деплой по changed-детекту
  (образец cats-challenge): джоба `changes` сравнивает git-пути (pull_request — merge
  base, push — `github.event.before`), компоненты backend (`backend/*`) и frontend
  (`app/`, `packages/`); правки пайплайна (`.forgejo/workflows/ci.yml`, `.ci/changes.py`)
  заставляют собирать всё. Джобы без setup-джоб, готовые образы, кэши
  GOMODCACHE/npm/cargo, resource limits контейнера (`options: --cpus 2 --memory 2g`):
  1. `build-backend` (if changes.outputs.backend) — go vet, gofumpt gate, go build,
     go test (sqlite) + пуш образа `git.cu3rd.ru/cu3rd-private/yc-hack-horses:<sha>`
     (репо приватное; push только на master; пока `backend/Dockerfile` нет — черновой
     образ с `/health`);
  2. `build-frontend` (if changes.outputs.frontend) — npm ci + npm run build в `app/`
     (сборка клиентов в CI; в self-service клиенты не деплоятся);
  3. `build-desktop` — cargo check, guarded по `hashFiles`, пока каталога нет;
  4. `deploy` — только push в master, `enable-openid-connect: true`,
     шаг Propose только если `build-backend.result == 'success'`, `always() && !cancelled()`
     в if джобы, чтобы skipped не блокировал.
- Makefile: цели `compose-logs`, `compose-status` (+`.PHONY`).
- README: разделы «Локальная разработка», «Центральная нода (docker compose)»,
  «Деплой в self-service», «Генерация dev CA».

## Деплой

Проект self-service: `projects/yc-hack-horses.yaml` — env prod, зона grm, квота
cpu 2 / memory 2Gi / storage 10Gi, домен `yc-hack-horses.cu3rd.ru`, порт 8080,
healthcheck `/health`, postgres-ресурс (`DATABASE_URL`), serviceAnnotations h2c
для нативного gRPC за Traefik, variables из `.env.example` без секретов.
Секреты: `SELF_SERVICE_DELIVERY_TOKEN` в Actions репо (выдан `ss token`).
Пароль БД приходит внутри `DATABASE_URL` из ресурса postgres — в `variables` не дублируется.

## Замечания

- Первая доставка идёт с черновым образом (`/health` 200), как только бэкенд-агент
  добавит `backend/Dockerfile` (CMD web) в master, CI начнёт собирать реальный образ
  без изменений CI.
- Прогон gofumpt/gate до пуша выполнен (`make backend-fmt` чисто).

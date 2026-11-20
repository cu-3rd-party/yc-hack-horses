# README — yc-hack-horses

Распределённая offline-first система экологического мониторинга лошадей Пржевальского.

## Состав монорепозитория
- `backend/` — Go-нода: один Sync Engine, SQLite (local) / PostgreSQL (central), REST API, gRPC sync, mTLS.
- `app/` — React + TypeScript + Vite + shadcn/ui, Feature-Sliced Design.
- `desktop/` — Tauri, Go-нода как sidecar.
- `mobile/` — React Native + Go Core bridge (foundation).
- `packages/contracts/` — общие TS-типы и API-контракты.
- `infra/` — Docker Compose, dev-сертификаты, скрипты.

## Ключевое правило
Central нода определяется **только** по hostname `yc-hack-horses.cu3rd.ru` + успешной TLS-проверке,
не по конфигу или полю role. Все межнодовые соединения — mTLS.

## Быстрый старт
- Центральная нода: `make compose-up` (см. `infra/compose`).
- Dev-сертификаты: `make dev-certs` (development-only).
- Локальная нода: `make backend && ./bin/node --config node.yaml` (см. `docs/development`).
- Frontend: `make app-install && make app-build`.

## Локальная разработка

```sh
make backend          # сборка ноды в bin/node
cp backend/config.example.yaml node.yaml   # реальные значения правишь локально, в git не коммитится
./bin/node --config node.yaml
make backend-test     # тесты (sqlite, без сервисов)
make app-install app-build   # фронтенд клиента
```

`config.example.yaml` — образец конфига ноды: локальный REST на `127.0.0.1:8080`,
межнодовый gRPC на `:9443`. Central нода определяется только по hostname
`yc-hack-horses.cu3rd.ru` и успешной mTLS-проверке, не по конфигу.

## Центральная нода (docker compose)

```sh
cp .env.example .env          # заполни POSTGRES_PASSWORD
make dev-certs                # mTLS-сертификаты в infra/certs (development-only)
make compose-up               # postgres + central node (порт 8080)
make compose-logs             # логи ноды
make compose-status           # статус стеков
make compose-down             # остановить
```

Схема — `infra/compose/docker-compose.yml`: postgres с volume и healthcheck,
нода с env из `.env` и mTLS-секретами read-only mount `/run/secrets/node.{crt,key}`,
`/run/secrets/ca.crt` (секреты не пекутся в образ). Перед первым запуском нода
накатывает миграции на postgres (DATABASE_URL из `.env`); при обновлении образа
миграции применяются на старте автоматически.

YOLO-инференс требует CGO-сборку (desktop/mobile бинарь); контейнер центральной
ноды собирается CGO-free — инференс деградирует в `ErrModelUnavailable` (ADR-0012).

## Деплой в self-service

Central нода живёт в self-service как проект `yc-hack-horses` (env `prod`, зона `grm`,
домен `yc-hack-horses.cu3rd.ru`, порт 8080, healthcheck `/health`), Postgres — платформенный
ресурс, `DATABASE_URL` приходит из него. Push в `master` запускает `.forgejo/workflows/ci.yml`:
сборка+тест backend, сборка клиентского фронтенда (`app/`), проверка desktop-клиента,
затем джоба `deploy` публикует образ `git.cu3rd.ru/cu3rd-private/yc-hack-horses:<sha>` и
вызывает деплой self-service (project/environment/app). Статус: `ss status yc-hack-horses prod`.

## Релизы (тег v*)

Push тега `v*` запускает `.forgejo/workflows/release.yml`: собираются
`backend-linux-amd64.tar.gz` (бинарь ноды + config.example.yaml + README), десктоп-клиент
(.deb и .AppImage через tauri build; сборщик — публичный CI-образ
`git.cu3rd.ru/cu3rd/yc-hack-horses-ci/tauri-base:latest`, исходник
`infra/docker/tauri-base.Dockerfile`) и android APK (expo prebuild + gradle,
release-APK подписан debug-ключом — Play-ready подписи нет). Джоба
`release-publish` создаёт **draft-релиз** через Forgejo API (секрет `RELEASE_TOKEN`
в Actions репо) и прикладывает артефакты. Релиз остаётся черновиком — публикуй
после проверки вручную; автоматическая публикация — поставь `"draft":false`.

## Генерация dev CA

```sh
make dev-certs   # bash infra/scripts/gen-dev-certs.sh — development-only
```

Скрипт создаёт dev CA и узловые сертификаты в `infra/certs/`; они игнорируются git
(`*.crt`, `*.key`). Используются docker compose (secret-файлы) и локальными нодами
для mTLS. Для продакшена сертификаты выдаёт платформа, dev CA нельзя доверять там.

## Документация
- ADR: `docs/adr/`
- Протокол синхронизации: `backend/api/proto/sync/v1/sync.proto`

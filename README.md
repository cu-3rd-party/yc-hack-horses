# README — yc-hack-horses

Распределённая offline-first система экологического мониторинга лошадей Пржевальского.

## Состав монорепозитория
- `backend/` — Go-нода: один Sync Engine, SQLite (local) / PostgreSQL (central), REST API, gRPC sync, mTLS.
- `app/` — настольный полевой журнал на Electron, React, TypeScript, Tailwind CSS v4 и shadcn/ui. React Konva отображает признаки. Рабочий журнал использует HTTP API локальной Go-ноды. Демосценарии используют отдельный архив IndexedDB.
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
- Настольное приложение требует Node.js 22.12+ и npm. Из каталога `app/` выполните `npm ci`, затем `npm start`.
- Для сборки приложения Apple Silicon выполните `npm run package:mac`. Пакет находится в `app/release/mac-arm64/yc-hack-horses.app`. Для Linux используйте `npm run package:linux`.
- Установленное приложение запускает встроенную Go-ноду с SQLite и ONNX Runtime. Если локальная нода уже отвечает на `127.0.0.1:8080`, приложение подключается к ней. В режиме разработки (`npm start`) ноду нужно запустить отдельно.
- Для сравнения своих снимков выключите «Демосценарии» и откройте «Распознавание». Каждый файл модели скачивается отдельно. Для MegaDescriptor и масок частей тела укажите HTTPS-ссылки на файлы с ожидаемыми контрольными суммами. Встроенная нода хранит базу, фотографии и веса в каталоге `journal` профиля приложения. [Контракт сравнения и файлы весов](docs/comparison-api.md).
- Чтобы попробовать демонстрацию, включите «Демосценарии» в настройках. В разделе «Распознавание» выберите «Табун у сопки», подтвердите первую лошадь и сохраните вторую как новую особь. Результаты появятся в галерее и журнале. Управление разовым сбоем и сброс данных находятся в настройках. Снимки, модель и сетевой обмен демонстрационные, все операции выполняются локально.
- `npm test` проверяет поведение архива. После `npm run build` выполните `npm run test:desktop` для проверки Electron на машине с графической сессией. Переменная `JOURNAL_BINARY` позволяет проверить исполняемый файл упакованного приложения. Тест использует временный профиль.

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

## Документация API (Scalar, только central)

На центральной ноде можно включить интерактивную документацию REST API
(Scalar UI на `/api/scalar`, спека OpenAPI 3 — `/api/scalar/openapi.yaml`):

```sh
API_DOCS_ENABLED=true          # env, либо api.docs_enabled: true в node.yaml
```

По умолчанию флаг выключен (`api.docs_enabled: false` в `backend/config.example.yaml`);
локальные ноды его не включают — документация публикуется только на central.
После включения: `https://yc-hack-horses.cu3rd.ru/api/scalar` (UI забирает спеку
с локального эндпоинта, наружу ходит только браузерная оболочка с CDN).
Валидность спеки и 404 без флага проверяются тестом `docs_test.go` в
`backend/internal/transport/http`.

## Деплой в self-service

Central нода живёт в self-service как проект `yc-hack-horses` (env `prod`, зона `grm`,
домен `yc-hack-horses.cu3rd.ru`, порт 8080, healthcheck `/health`), Postgres — платформенный
ресурс, `DATABASE_URL` приходит из него. Push в `master` запускает `.forgejo/workflows/ci.yml`:
сборка+тест backend, сборка клиентского фронтенда (`app/`), проверка desktop-клиента,
затем джоба `deploy` публикует образ `git.cu3rd.ru/cu3rd-private/yc-hack-horses:<sha>` и
вызывает деплой self-service (project/environment/app). Статус: `ss status yc-hack-horses prod`.

## Релизы (тег v*)

Для отдельного выпуска настольного приложения создайте на коммите frontend тег `desktop-v<версия>`. Версия должна совпадать с `app/package.json`. После отправки тега в origin Forgejo собирает клиент и публикует ZIP для Apple Silicon, AppImage и deb для Linux x86-64 и SHA256SUMS на [странице выпусков](https://git.cu3rd.ru/cu3rd/yc-hack-horses/releases). Пакет macOS подписан ad hoc. Для запуска загруженного приложения macOS может запросить подтверждение в настройках конфиденциальности и безопасности.

Push тега `v*` запускает `.forgejo/workflows/release.yml`: собираются
`backend-linux-amd64.tar.gz` (бинарь ноды + config.example.yaml + README), десктоп-клиент
(.deb и .AppImage через electron-builder) и android APK (expo prebuild + gradle,
release-APK подписан debug-ключом — Play-ready подписи нет). Джоба
`release-publish` создаёт **draft-релиз** через Forgejo API (секрет `RELEASE_TOKEN`
в Actions репо) и прикладывает артефакты. Релиз остаётся черновиком — публикуй
после проверки вручную; автоматическая публикация — поставь `"draft":false`.

### Процедура релиза (проверено на тестовом прогоне v0.0.0-test)

1. Убедись, что master зелёный (ci.yml), и тегируй: `git tag vX.Y.Z origin/master
   && git push origin vX.Y.Z`.
2. Следи за прогоном: `GET /api/v1/repos/cu3rd/yc-hack-horses/actions/tasks`
   (джобы `release: backend tar.gz`, `desktop`, `mobile APK`, `release-publish`).
   Backend — обязательный; desktop/mobile падения не блокируют релиз, но тогда
   артефактов .deb/.AppImage/.apk не будет — чини и перетегируй.
3. Проверь драфт: `GET /repos/…/releases` → запись с `"draft": true`, в assets —
   `backend-linux-amd64.tar.gz` минимум (при удачных сборках ещё `.deb`,
   `.AppImage`, `.apk`).
4. Публикация — вручную в UI (или `PATCH .../releases/{id}` с `"draft": false`).
5. Откат незадокументированного теста: `DELETE /repos/…/releases/{id}` затем
   `DELETE /repos/…/tags/<tag>` (порядок: сначала релиз, иначе тег занят).
   Перетегировать после фикса воркфлоу: `git tag -f <tag> && git push -f origin <tag>`.

Грабли, отловленные на тестовом прогоне: `actions/upload-artifact@v4`/`download-artifact@v4`
не работают на Forgejo (GHES-only) — только v3;
`sdkmanager` после установки cmdline-tools нужно добавлять в `PATH`; gradle в
контейнере с лимитом памяти — `--no-daemon -Dorg.gradle.jvmargs=-Xmx2048m`.

## Генерация dev CA

```sh
make dev-certs   # bash infra/scripts/gen-dev-certs.sh — development-only
```

Скрипт создаёт dev CA и узловые сертификаты в `infra/certs/`; они игнорируются git
(`*.crt`, `*.key`). Используются docker compose (secret-файлы) и локальными нодами
для mTLS. Для продакшена сертификаты выдаёт платформа, dev CA нельзя доверять там.

## mTLS-сертификаты центральной ноды в self-service

Серверный серт+ключ центральной ноды (`central.crt`/`central.key`) и dev CA
(`ca.crt`) задеплоены как sealed secret `yc-hack-horses-prod-web-files`
(namespace `cu3rd-yc-hack-horses-prod`, ключи `node.crt`/`node.key`/`ca.crt`),
монтируются read-only в `/run/secrets/` (values `sealedFiles` чарта `charts/app`,
см. self-service). Env ноды: `CERT_FILE=/run/secrets/node.crt`,
`KEY_FILE=/run/secrets/node.key`, `CA_FILE=/run/secrets/ca.crt`
(Actions-переменные репо), gRPC слушает `0.0.0.0:9443` (`GRPC_LISTEN_ADDR`) —
внутри пода, наружу Service/Ingress не открывается.

- **Как проверяется central identity** — ADR-0007 (mTLS trust model) и ADR-0008
  (central node identification): central определяется только по hostname
  `yc-hack-horses.cu3rd.ru` + успешной TLS-проверке серверного серта; клиентские
  серты на gRPC-сервере проверяются против CA (`tls.RequireAndVerifyClientCert`,
  `ClientCAs` из `CA_FILE`) с дополнительной привязкой к node_id по URI SAN.
- **Проверка после доставки**: под Running, `/health` → 200, в логах нет warning
  про `ca.crt`; gRPC поднят с `mtls=true` (строка `grpc listening` в логах).
- **Замена dev-CA на production**: перегенерировать сертификаты платформенным CA
  (enrollment — ADR-0007), пересоздать секрет `kubeseal`'ом
  (`kubeseal -f plain.yaml --controller-name sealed-secrets-controller
  --controller-namespace kube-system -o yaml`), заменить блок `sealedFiles.keys`
  в `projects/yc-hack-horses.yaml` (имена ключей `node.crt`/`node.key`/`ca.crt`
  и пути не меняются — под перезапустится на новой checksum. Dev-CA никуда не
  добавлять: `security.Config.Validate` запрещает пустой `ca_file`
  (self-signed auto-trust запрещён).

## gRPC за ingress (grpc_multiplex)

Центральная нода в self-service живёт за Traefik ingress: наружу открыт только
443, TLS терминируется на ingress, а бэкенд получает запросы по h2c. Чтобы
gRPC-клиенты работали через тот же домен (`yc-hack-horses.cu3rd.ru:443`), включён
мультиплекс-режим:

- `network.grpc_multiplex: true` (env `GRPC_MULTIPLEX`) — на
  `network.listen_addr` (8080) работает обёртка
  `golang.org/x/net/http2/h2c`: запросы HTTP/2 с `content-type: application/grpc`
  уходят в gRPC-сервер синхронизации, всё остальное — в REST-роутер.
- Прямой mTLS-порт `GRPC_LISTEN_ADDR` (9443) остаётся как есть (mTLS при
  настроенных сертификатах) — для прямых подключений внутри кластера он не
  задействован ingress'ом.
- Аутентификация через ingress: Traefik-middleware `passTLSClientCert`
  (`pem: true`) прокидывает клиентский серт в заголовке
  `X-Forwarded-Tls-Client-Cert`; gRPC-интерцепторы мультиплекс-режима проверяют
  его против `security.ca_file` (цепочка, срок действия, URI SAN
  `przewalski://node/<id>`, полномочия SendEvents+ReceiveEvents). Нет заголовка
  или невалидный серт → gRPC `Unauthenticated`. Traefik выставляет этот заголовок
  из состояния TLS-соединения, поэтому значение, подделанное самим клиентом,
  игнорируется (в установленной версии Traefik CRD v3.7 у passTLSClientCert нет
  опции `perms.flag` — подмена отсекается самим фактом перезаписи заголовка).
  Для запроса клиентского серта ingress'ом на роутере включён TLSOption с
  `clientAuth: {clientAuthType: RequestClientCert}` (сёрт не проверяется на
  ingress — полную проверку выполняет бэкенд против dev-CA).
- `grpc_multiplex` требует `security.ca_file` — без CA конфиг отвергается
  (fail closed).

## Документация
- ADR: `docs/adr/`
- Протокол синхронизации: `backend/api/proto/sync/v1/sync.proto`

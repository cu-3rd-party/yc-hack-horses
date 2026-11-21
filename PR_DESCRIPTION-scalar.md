# PR: Scalar Swagger UI на /api/scalar (флаг api.docs_enabled)

## Что сделано

- **OpenAPI-спека** (`backend/internal/transport/http/docs.go`): документ OpenAPI
  3.0.3 собирается декларативно в Go и отдаётся как YAML на
  `GET /api/scalar/openapi.yaml`. Схемы: Horse, Observation, TraitSnapshot,
  TraitSnapshotResponse, Blob, NodeInfo, SimilarResponse, Error + тела запросов
  (CreateObservationRequest, CreateTraitsRequest, ConfirmTraitsRequest).
- **Scalar UI**: `GET /api/scalar` отдаёт один статический HTML-шаблон, который
  грузит `@scalar/api-reference` с CDN (`cdn.jsdelivr.net`) и читает спеку с
  локального эндпоинта `/api/scalar/openapi.yaml` — наружу ходит только
  браузерная оболочка, сама спека генерируется нодой.
- **Флаг**: `api.docs_enabled` (default `false`) + env `API_DOCS_ENABLED`
  (`backend/internal/config`, `backend/config.example.yaml`, прокидывается в
  роутер через `Deps.DocsEnabled` из `internal/app/app.go`). Маршруты
  документации монтируются только при `true`; у локальных нод — 404.

## Почему этот подход

- **swag/swag (аннотации)** требует codegen-шага в CI (`swag init`) и
  аннотаций поверх каждого хендлера — лишний инструмент в сборке ради одного
  статичного документа.
- **swaggest/rest** требует переписать хендлеры под его структуру — чужая зона.
- Выбран **статический описательный вариант**: спека — обычный Go-код
  (структуры + `gopkg.in/yaml.v3`, уже в зависимостях), без новых библиотек и
  без генерации на этапе сборки. Дрейф спеки от роутера ловит тест
  `docs_test.go`: он разворачивает роутер через `httptest`, проверяет 404 без
  флага, 200 + HTML с `data-url` с флагом, парсит YAML и требует наличия всех
  фактических путей и методов, а также ключевых схем.

## Эндпоинты в спеке

`/health`, `/readyz`, `/api/v1/horses` (GET/POST), `/api/v1/horses/{id}`
(GET/PATCH), `/api/v1/observations` (GET/POST, query horse_id),
`/api/v1/blobs` (GET/POST multipart, дедупликация 200/201, 413),
`/api/v1/blobs/{sha256}` (GET, бинарное содержимое, ETag),
`/api/v1/node` (GET), `/api/v1/events` (GET — WebSocket, помечен описанием
апгрейда, не SSE), `/api/v1/inference/traits` (POST),
`/api/v1/trait_snapshots/{id}` (PATCH), `/api/v1/inference/similar` (GET,
query blob_id/limit).

## Тесты

- `TestScalarDisabled404` — без флага `/api/scalar` и `openapi.yaml` → 404.
- `TestScalarEnabled` — с флагом: 200 HTML, 200 YAML, валидный OpenAPI 3
  (парсинг yaml.v3), наличие всех путей/методов и схем
  Horse/Observation/TraitSnapshot/Blob/NodeInfo/Error.
- `go build ./...`, `go vet`, `go test ./...` (backend), `gofumpt` — чисто.

## Включение на central

В деплое self-service — переменная окружения `API_DOCS_ENABLED=true`
(проектные переменные ведёт координатор; локальная нода держит `false`).
После включения: `https://yc-hack-horses.cu3rd.ru/api/scalar`.
README обновлён разделом «Документация API (Scalar, только central)».

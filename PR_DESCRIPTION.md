# feat/cv-foundation — контрактная основа локального inference (Agent H)

## Что сделано
Контрактная основа локального CV-inference без обученной модели (ADR-0012).

### Интерфейс движка (backend/internal/inference)
- `InferenceEngine`: `ExtractTraits(ctx, ImageInput) (TraitResult, error)`, `Embed(ctx, ImageInput) (EmbeddingResult, error)`.
- `ImageInput` — чтение blob по sha256 через BlobStore (или Reader).
- `TraitResult` — model_version, канонический набор признаков (head/muzzle/ears/coat/mane/proportions/markings/scars с допустимыми значениями), confidence.
- `EmbeddingResult` — model_version, вектор.
- `ModelMetadata` — версия, input/output schema, preprocess spec (константы в коде).
- `UnavailableEngine` — заглушка по умолчанию: всегда `ErrModelUnavailable`, приложение не блокируется.
- `ReferenceEngine` — детерминированный baseline на цветовых гистограммах по областям (stdlib image/*). В README пакета явно помечено: baseline, НЕ обученная модель, НЕ идентификация.
- Точка расширения ONNX (V1) задокументирована (README пакета + ADR-0012): подключение onnxruntime-go в реализации InferenceEngine.

### REST (аддитивно, хуки в server.go — 3 маршрута + поле Deps.Engine)
- `POST /api/v1/inference/traits` — предсказание по blob_id (или sha256) → TraitResult с model_version и confidence; сохраняет снимок trait_snapshots (confirmed=false).
- `PATCH /api/v1/trait_snapshots/{id}` — подтверждение человеком: оригинал предсказания в `raw_predicted_traits` (миграция 002), правка в `traits`, `edited=true`, повторные правки не затирают оригинал.
- `GET /api/v1/inference/similar?blob_id=...&limit=...` — поиск по fingerprint среди подтверждённых снимков; ответ содержит `disclaimer` (fingerprint — поиск похожих, НЕ биологическая идентичность).

### Прочее
- Миграции `backend/migrations/{sqlite,postgres}/002_trait_snapshots_raw.sql` — колонка `raw_predicted_traits`.
- `domain.TraitSnapshot` — поле `RawPredictedTraits`.
- Новый файл `backend/internal/storage/sqlstore/inference.go` (аддитивно, без правок существующих файлов sqlstore): GetBlobByID, GetTraitSnapshot, ConfirmTraitSnapshot, ListSimilarTraitSnapshots, FindSnapshotByBlobSHA.
- Расширен `docs/adr/0012-local-inference.md`; README пакета inference.
- Зоны вне владения (infra/, app/, desktop/, packages/, sync, security, storage-файлы) не изменены.

## Тесты
- `backend/internal/inference/traits_test.go` — fingerprint: одинаковый набор → одинаковый fingerprint, порядок ключей/регистр не важны; канонизация отбрасывает неизвестные ключи и недопустимые значения; набор CanonicalKeys полный.
- `backend/internal/inference/reference_test.go` — ReferenceEngine на синтетическом изображении (генерируется в тесте): признаки, детерминизм embedding, невалидный вход, работа через BlobStore, UnavailableEngine.
- `backend/tests/integration/inference_test.go` — httptest: предсказание → подтверждение человеком → проверка что оригинал сохранён в raw_predicted_traits; similar-поиск с дисклеймером; роутер без движка → 503 (не блокируется).

## Проверка
`go build ./...`, `go vet ./...`, `go test ./...` — чисто; gofumpt применён.

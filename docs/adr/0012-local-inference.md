# ADR-0012: Local inference architecture
**Status:** accepted

## Решение
Inference — локально на устройстве через интерфейс `internal/inference.InferenceEngine` (ExtractTraits / Embed). V0: определены контракты (TraitResult, EmbeddingResult, version, confidence), pipeline предобработки изображений и заглушка «модель недоступна» — приложение работает без модели. Формат — ONNX (onnxruntime-go на desktop). Никакой заявленной обученности на лошадях Пржевальского, пока веса не подтверждены.

## Почему
Отсутствие модели не должно блокировать offline-запись наблюдений: признаки можно подтвердить человеком.

## Расширение (V0.1, feat/cv-foundation)

### Канонический набор признаков
Канонические ключи: `head`, `muzzle`, `ears`, `coat`, `mane`, `proportions`, `markings`, `scars`. Для каждого ключа задан закрытый набор допустимых значений (`inference.AllowedValues`); канонизация отбрасывает неизвестные ключи и недопустимые значения, регистр не важен.

### Fingerprint и Re-id поиск
`fingerprint` — sha256 отсортированного канонизированного набора признаков: одинаковый набор → одинаковый fingerprint, порядок ключей не важен. Используется **только** для поиска похожих снимков (`GET /api/v1/inference/similar?blob_id=...&limit=...`). Это **НЕ биологическая идентичность** — ответ всегда содержит поле `disclaimer`.

### Human-in-the-loop подтверждение
`POST /api/v1/inference/traits` (по `blob_id` или sha256) сохраняет снимок `trait_snapshots` (confirmed=false, fingerprint). `PATCH /api/v1/trait_snapshots/{id}` — подтверждение человеком: исходное предсказание сохраняется в `raw_predicted_traits` (миграция 002), правка — в `traits`, `edited=true`, `confirmed=true`, confidence = 1.0. Повторные правки не затирают оригинал.

### Model metadata (в коде, единый источник истины)
`inference.ModelMetadata` (referenceMetadata, версия `reference-v1`):
- Input schema: `{"type":"image","formats":["png","jpeg","gif"],"source":"blob_store","key":"sha256"}`
- Output schema: `{"traits":"map[string]string","confidence":"float64","embedding":"[]float32"}`
- Preprocess spec: 256x256, RGB, NHWC, нормализация 0..1.

### Движки
- `UnavailableEngine` — заглушка по умолчанию (Engine=nil в Deps): всегда `ErrModelUnavailable` → REST отвечает 503 с подсказкой «введите признаки вручную»; приложение не блокируется.
- `ReferenceEngine` — **baseline** на цветовых гистограммах по областям (stdlib image/*). Работает, но это **НЕ обученная модель** и **НЕ идентификация**; confidence жёстко 0.3, подтверждение человеком обязательно. Подробности — backend/internal/inference/README.md.

### Точка расширения ONNX (V1)
Обученная модель подключается реализацией `InferenceEngine` на onnxruntime-go: предобработка по `PreprocessSpec`, вход по `ImageInput.SHA256` из BlobStore. Контракт движка и REST-эндпоинты не меняются.

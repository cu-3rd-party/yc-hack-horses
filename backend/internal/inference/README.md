# internal/inference — контрактная основа локального CV-inference (ADR-0012)

## ВАЖНО: baseline, НЕ обученная модель

`ReferenceEngine` — детерминированная эвристика на цветовых гистограммах по
горизонтальным областям изображения (stdlib `image/*`). Это **baseline**:

- **НЕ обученная модель** — никакие веса не используются;
- **НЕ идентификация** животных — fingerprint это поиск похожих наборов
  признаков, не биологическое ID;
- confidence жёстко занижен (0.3), подтверждение человеком обязательно.

## Контракт

```go
type InferenceEngine interface {
    ExtractTraits(ctx context.Context, input ImageInput) (TraitResult, error)
    Embed(ctx context.Context, input ImageInput) (EmbeddingResult, error)
}
```

- `ImageInput` — чтение blob по sha256 через BlobStore (или Reader).
- `TraitResult` — model_version, канонический набор признаков
  (head/muzzle/ears/coat/mane/proportions/markings/scars), confidence 0..1.
- `EmbeddingResult` — model_version, вектор (64 для baseline).

`UnavailableEngine` всегда возвращает `ErrModelUnavailable`; приложение не
блокируется — человек вводит признаки сам (ADR-0012). Роутер по умолчанию
использует именно его.

## Точка расширения ONNX (V1)

Обученная модель подключается **здесь**, в реализации `InferenceEngine`:
использовать [onnxruntime-go](https://github.com/yalue/onnxruntime_go) —
предобработка по `ModelMetadata.PreprocessSpec` (256x256, RGB, NHWC, 0..1),
вход по `ImageInput.SHA256` из BlobStore, выход — признаки + embedding.
Контракт движка и REST-эндпоинты при этом не меняются.

## Канонизация и fingerprint

`Fingerprint(traits)` — sha256 отсортированного канонизированного набора:
неизвестные ключи и недопустимые значения отбрасываются, регистр не важен.
Одинаковый набор → одинаковый fingerprint. Используется только для поиска
похожих снимков (`GET /api/v1/inference/similar`), с дисклеймером в ответе.

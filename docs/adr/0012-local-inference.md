# ADR-0012: Local inference architecture
**Status:** accepted
## Решение
Inference — локально на устройстве через интерфейс `internal/inference.InferenceEngine` (ExtractTraits / Embed). V0: определены контракты (TraitResult, EmbeddingResult, version, confidence), pipeline предобработки изображений и заглушка «модель недоступна» — приложение работает без модели. Формат — ONNX (onnxruntime-go на desktop). Никакой заявленной обученности на лошадях Пржевальского, пока веса не подтверждены.
## Почему
Отсутствие модели не должно блокировать offline-запись наблюдений: признаки можно подтвердить человеком.

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
Обученная модель подключается реализацией `InferenceEngine` на onnxruntime-go: предобработка по `ModelMetadata.PreprocessSpec` (256x256, RGB, NHWC, 0..1), вход по `ImageInput.SHA256` из BlobStore, выход — признаки + embedding. Контракт движка и REST-эндпоинты при этом не меняются.

## Расширение (V1, feat/yolo-cv): YOLO-детекция + кроп, НЕ трейты

### Решение
`backend/internal/inference/yolo.YoloEngine` — реализация `InferenceEngine` на предобученной **YOLOv8n** (ONNX, ultralytics release v8.4.0, COCO). Инференс — onnxruntime-go (`github.com/yalue/onnxruntime_go` v1.38.0, ORT API 31):

- **Детекция, не трейты.** YOLO выдаёт прямоугольную область животного (COCO class `horse` = 17). Обученности на лошадях Пржевальского **нет и не заявляется**.
- **Пайплайн:** letterbox 640x640 (серый 114) → NCHW 1x3x640x640, 0..1 → NMS-free head (1x84x8400) → фильтр класса 17 → NMS (IoU 0.45) → при мультивидных лошадях берётся самая уверенная детекция.
- **Кропы:** весь корпус (bbox ±5%) и голова/морда (верхние 30% bbox, центральные 60% — эвристика стоящей лошади). Кропы передаются в делегирующую стратегию `TraitDelegate` (по умолчанию — гистограммная эвристика по областям, аналог ReferenceEngine, но по кропам). Результат — предзаполнение: снимок `trait_snapshots` с `confirmed=false`, человек подтверждает/правит (human-in-the-loop уже есть).
- **Embed:** детерминированный вектор из пуллинга пикселей кропа (сетка 4x4 средних RGB + глобальные средние, 64 значения) — НЕ вектор нейросети: детектор не даёт эмбеддингов.
- **Ответ строго содержит:** `model_version` (`yolov8n-coco-v1`), `confidence` = уверенность детекции, и дисклеймер REST-ответа: «предзаполнение — НЕ идентификация; требует подтверждения человеком».
- **Конфиг:** `inference.engine: yolo | reference | unavailable`, `inference.model_path`, `inference.runtime_lib`. Если веса или onnxruntime-библиотека недоступны — `ErrModelUnavailable`, приложение продолжает работать (человек вводит признаки сам).
- **Нет лошади на фото** — `inference.ErrNoAnimalDetected` → REST 400 «введите признаки вручную».

### Веса и лицензия
- yolov8n.onnx — release-asset [ultralytics/assets v8.4.0](https://github.com/ultralytics/assets/releases/download/v8.4.0/yolov8n.onnx), sha256 `b2bc52f40e8e1c532427d5bde3575a5d5b571b739fab2c6df443733ed1589cbd`.
- **Лицензия AGPL-3.0 (ultralytics):** применимость оценена как приемлемая для этой интеграции с оговоркой: веса не распространяются как часть репозитория (лежат в `infra/models/`, вне git, скачиваются `infra/scripts/download-yolo-model.sh`), проект — offline-first внутренний инструмент, YOLO-код (обучение/экспорт) не копируется в репозиторий — используется только бинарный ONNX-файл. Если AGPL-сканирование CI или распространение desktop-приложения станет проблемой — альтернатива: модели ONNX model zoo (MIT) или yolov9/yolo-world с Apache-2.0-выходами; замена — только веса и константы `yolo.ModelVersion`, контракт не меняется.
- onnxruntime shared library — NuGet `Microsoft.ML.OnnxRuntime` 1.31.1 (GitHub-релизы microsoft/onnxruntime недоступны с 2026-11-17; NuGet-пакет остаётся источником).

### Тесты
Юнит: letterbox/NMS/декодер на матрицах; кропы; deterministic embedding. Пайплайн на реальной модели — прогон на фото лошади (результат зафиксирован в PR_DESCRIPTION-node-yolo.md), на синтетическом изображении — детекция отсутствия с корректной ошибкой. Интеграционные: эндпоинты с YoloEngine через httptest (503 без модели, снимок `confirmed=false`, оригинальное предсказание в `raw_predicted_traits`).

# Agent Y — YOLO-детекция лошади и предзаполнение трейтов (feat/yolo-cv)

## Что сделано

Реальная (не фейковая) детекция животного на фото через предобученную YOLO ONNX
с кропами и передачей в существующий human-in-the-loop контур trait_snapshots.

### Модель
- **YOLOv8n** (ultralytics), ONNX, release-asset [ultralytics/assets v8.4.0](https://github.com/ultralytics/assets/releases/download/v8.4.0/yolov8n.onnx)
- sha256: `b2bc52f40e8e1c532427d5bde3575a5d5b571b739fab2c6df443733ed1589cbd`
- Путь: `infra/models/yolov8n.onnx` — **вне git** (`.gitignore`), качается `infra/scripts/download-yolo-model.sh` (проверка sha256)
- **Лицензия AGPL-3.0 (ultralytics)**: решение зафиксировано в ADR-0012 — веса не распространяются как часть репо (скачиваются скриптом), используется только бинарный ONNX, YOLO-код не копируется. Альтернатива при проблемах: ONNX model zoo (MIT) — замена только весов и константы `yolo.ModelVersion`.

### Инференс
- `github.com/yalue/onnxruntime_go` v1.38.0 (cgo), onnxruntime shared library — NuGet `Microsoft.ML.OnnxRuntime` **1.31.1** (GitHub-релизы microsoft/onnxruntime недоступны с 2026-11-17, NuGet — рабочий источник; sha256 `a96f132e5eb5ca04e23a7d2fc1078fb95aa591e8d71d0f237702ece3a1e0f4ea`).
- Пайплайн: letterbox 640x640 (серый 114) → NCHW 1x3x640x640, 0..1 → выход 1x84x8400 → фильтр класса **17 (horse)** → NMS (IoU 0.45) → самая уверенная детекция.
- Кропы: корпус (bbox ±5%), голова/морда (верхние 30% bbox, центральные 60%).
- `TraitDelegate` — точка расширения: дефолтная гистограммная эвристика по кропам (аналог ReferenceEngine по областям).

### Честность (по ТЗ)
YOLO **не обучен на лошадях Пржевальского** и **не даёт трейтов** — только локацию животного. Ответ содержит `model_version` (`yolov8n-coco-v1`), `confidence` (уверенность детекции) и дисклеймер «предзаполнение — НЕ идентификация; требует подтверждения человеком». Снимки создаются с `confirmed=false`, оригинал сохраняется в `raw_predicted_traits`.

### Деградация
- Нет весов/библиотеки → `inference.ErrModelUnavailable` → REST 503, приложение работает (человек вводит признаки сам).
- Лошади нет на фото → `inference.ErrNoAnimalDetected` → REST 400 «введите признаки вручную».

## Реальный прогон на фото лошади

Фото: Wikimedia Commons, лошадь Пржевальского (`Przewalski's horse`, CC BY-SA).
Запуск: `YOLO_MODEL_PATH=infra/models/yolov8n.onnx YOLO_RUNTIME_LIB=infra/models/libonnxruntime.so.1.31.1 YOLO_TEST_IMAGE=<фото> go test -run TestPipelineRealModel -v ./internal/inference/yolo/`

```
REAL RUN: model=yolov8n-coco-v1 conf=0.927
  traits: coat=bay muzzle=light mane=light head=straight ears=erect
          proportions=compact markings=none scars=none
```

- Детекция: найдена, уверенность **0.927** (класс horse).
- Трейты: гистограммная эвристика по кропам — предзаполнение, ожидает подтверждения.
- Синтетический тест: серый прямоугольник — `ErrNoAnimalDetected` (корректная ошибка отсутствия).

## Тесты
- Юнит: letterbox (размер/паддинг/ресайз), декодер выхода, NMS/IoU, unletterbox, кропы, deterministic embedding — на матрицах.
- Пайплайн: на реальной модели (прогон выше) + отсутствие детекции на синтетике.
- Интеграционные (httptest): YoloEngine без модели → 503; ReferenceEngine → 201 с `confirmed=false`, `raw_predicted_traits` сохраняется при PATCH, дисклеймер в ответе; несуществующий blob → 404.
- `go build ./...`, `go vet ./...`, `go test ./...` — чисто; `gofumpt -l` — пусто.

## Изменения
- `backend/internal/inference/yolo/` — новый под-пакет (движок, letterbox, постпроцессинг, делегат, embed, тесты, README).
- `backend/internal/inference/inference.go` — аддитивно: `ErrNoAnimalDetected`.
- `backend/internal/config/config.go` — `inference.engine: yolo`, `model_path`, `runtime_lib`, env `INFER_MODEL_PATH`/`INFER_RUNTIME_LIB`.
- `backend/internal/app/app.go` — выбор YoloEngine по конфигу.
- `backend/internal/transport/http/inference.go` — аддитивно: дисклеймер в ответе создания снимка, 400 для `ErrNoAnimalDetected`.
- `backend/internal/transport/http/inference_yolo_test.go` — интеграционные тесты.
- `infra/scripts/download-yolo-model.sh`, `.gitignore` (infra/models/), `backend/config.example.yaml`.
- `docs/adr/0012-local-inference.md` — расширение: YOLO-детекция + кроп, НЕ трейты; лицензия AGPL.
- `backend/go.mod` / `go.sum` — `github.com/yalue/onnxruntime_go v1.38.0`.

## Чего не хватает для полного трейт-движка
1. Модель, обученная на лошадях Пржевальского (сейчас — COCO horse + гистограммная эвристика по кропам).
2. Эмбеддинг от нейросети (сейчас — детерминированный пиксельный пуллинг; нужен backbone с embedding-головой или сравнение фич).
3. Точная сегментация головы/морды (сейчас — эвристические доли bbox).
4. Калибровка confidence эвристики трейтов по подтверждённым снимкам (feedback loop из trait_snapshots).
5. Пороги классов/оборотов под фон реальных фото заповедника (сейчас — дефолтные 0.25/0.45).

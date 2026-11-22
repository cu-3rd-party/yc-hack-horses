# HorseID v2 → Go/ONNX в прод: handoff (ветка `dev2211`)

**Цель:** заменить в бэкенде распознавание на нашу модель (голова + тело, DINOv2 + обученные ArcFace-проекции),
не меняя фронтенд и API (`comparison.Candidate` и т.д.). **Ничего обучать не нужно**: обученные веса лежат в
`infra/reid-lab/export/`, базовые сети — публичные предобученные, скачиваются как есть.

## 1. Метрики (честные, отложенный test)

Данные — только последние 2 года (дата съёмки ≥ 2024-11-22), 595 фото. Разбиение `split.json` фиксированное:
train 254 / val 150 / **test 191**, группы = «поездки» (съёмки одной лошади ближе 7 дней — одна группа),
в test все 4 сезона. Модель выбрана 4-фолдовой групповой CV только на train+val; test посчитан один раз.
Лог: `results/final_eval.log`, `results/final_results.json`.

| Вариант (test, 191 фото) | top-1 | top-3 | macro top-1 |
|---|---|---|---|
| случайно | 7% | 20% | 7% |
| сырые эмбеддинги MiewID | 18% | 39% | 17% |
| **DINOv2-B + ArcFace, голова+тело** (обучение и банк — только последние 2 года) | **24%** | **71%** | **27%** |
| **то же + исторические фото (<2024-11) в обучении и банке** ← **ЭТО ВЫГРУЖЕНО в `export/`** | **37%** | **74%** | **42%** |

Сезонные аугментации и ч/б-вид при подборе не помогли и отброшены. Слабое место — смена сезона.

## 2. Пайплайн, который надо повторить в Go

```
фото (EXIF-ориентация, уменьшить до max 1280px по длинной стороне)
 → YOLO11s (COCO, класс 17 horse, conf 0.25, imgsz 640) → самый большой бокс = body
 → ViTPose+ base, эксперт AP-10K (dataset_index=3) на этом боксе → 17 точек
 → head box: точки {0 нос, 1,2 глаза, 3,4 уши} со score>0.3, нужны нос и хотя бы один глаз и ≥3 точки;
            квадрат по центру bbox точек, сторона = 1.6 × max(ширина,высота) bbox точек; отбросить если < 48 px
 → body crop = body box ± 8 px; head crop = head box (обрезать по кадру)
 → DINOv2-B на каждом кропе → вектор 768 → L2-норм
 → проекция ArcFace: e = normalize(W·x + b), W: 256×768 (отдельно head и body)
 → сходство с каждым фото банка: cos(e_q, e_g) для head и для body
 → счёт лошади по части = среднее двух лучших сходств по её фото (agg=top2; одно фото → само значение)
 → итог = 0.5·head + 0.5·body (если части нет — берётся другая)
 → проценты: softmax(20 · score) по 15 лошадям (не калиброваны)
```
Бок (`view`): нос левее центра бёдер (точки 11,12) больше чем на 0.25·ширина бокса → `left`, правее → `right`,
иначе `front`; нет носа, есть бёдра → `rear`. В ранжировании бок не используется (эффекта не дал), только для UI.

### Базовые сети (предобученные, НЕ дообучались)
| Сеть | Источник | Вход / нормализация |
|---|---|---|
| YOLO11s | `ultralytics` `yolo11s.pt` (экспорт: `YOLO("yolo11s.pt").export(format="onnx", imgsz=640)`) | 640, letterbox, 0..1 |
| ViTPose+ base | HF `usyd-community/vitpose-plus-base`, `dataset_index=3` (AP-10K) | 256×192 кроп бокса (affine как в `VitPoseImageProcessor`), ImageNet mean/std |
| DINOv2-B/14 | timm `vit_base_patch14_dinov2.lvd142m`, `img_size=224`, `num_classes=0` (выход = CLS после norm, 768) | resize **224×224 bicubic без кропа**, ImageNet mean (.485,.456,.406) std (.229,.224,.225) |

Экспорт DINOv2 в ONNX:
```python
import timm, torch
m = timm.create_model("vit_base_patch14_dinov2.lvd142m", pretrained=True, num_classes=0, img_size=224).eval()
torch.onnx.export(m, torch.randn(1,3,224,224), "dinov2-b14-224.onnx", input_names=["pixel_values"],
                  output_names=["embedding"], dynamic_axes={"pixel_values":{0:"b"},"embedding":{0:"b"}}, opset_version=17)
```
ViTPose: `transformers.VitPoseForPoseEstimation` → ONNX (вход `pixel_values` [1,3,256,192] + `dataset_index` фиксировать 3
при экспорте, обернув модель), декод heatmap → argmax + аффинный обратный перенос (см. `post_process_pose_estimation`).
**Упрощение, если ViTPose не успеваете:** head crop можно брать из уже существующей в бэкенде part-маски `head`
(`reid/parts.go`, DINOv2-small part heads) — но тогда качество надо перепроверить, наши веса обучены на ViTPose-кропах.

### Обученные веса (в git, `infra/reid-lab/export/`)
| Файл | Формат |
|---|---|
| `heads_dinov2_head.f32`, `heads_dinov2_body.f32` | little-endian float32: `W[256][768]` построчно, затем `b[256]` |
| `gallery_dinov2_head.f32`, `gallery_dinov2_body.f32` | float32 `[1114][256]`, уже спроецированные и нормированные векторы фото банка; строки с NaN = части нет |
| `gallery.json` | 1114 строк: `photo_id` (id в `meta.csv`), `horse`, `date`, `view` — порядок = строки gallery-файлов |
| `meta.json` | конфиг (`bb=dinov2, w=0.5, agg=top2`), список 15 кличек, температура 20 |

Проверка порта: для фото из `test_photos` (их нет в банке) векторы Go и Python (`demo_app.py`) должны совпадать
с cos > 0.999, а top-3 — совпадать по именам.

## 3. Как встроить в бэкенд без изменения фронта

Сейчас `comparison.Rank` сравнивает только векторы **одной** `model_version` с эталонами из БД (`Reference.Descriptor`).
Варианты:
1. **Нативно (правильно):** новый `InstanceDetector`/`Analyzer` в `backend/internal/comparison` + `inference/reid`:
   возвращает `Prediction` с `Descriptor{Version: "horseid-dinov2-arcface-v2", Vector: concat(head256, body256)}`;
   в `Rank` для этой версии считать head/body отдельно (первые/вторые 256), `top2`-агрегацию и `0.5/0.5` слияние.
   Эталоны: перекодировать подтверждённые фото лошадей этой версией (миграция/джоба) **или** на старте загружать
   `export/gallery_*.f32` как эталоны (photo_id → horse по кличке).
2. **Быстрый мост (уже есть в коде):** `INFER_COMPARISON_URL` → `comparison.ModelEndpoint` шлёт байты фото на HTTP-сервис и
   ждёт `{model_version,width,height,detections:[{detection_confidence,horse_polygon,traits,embedding}]}`.
   Можно поднять `demo_app.py` (Python) как сервис и отдать в `embedding` вектор; но эталоны в БД должны иметь ту же версию.

**Внимание:** отдельного dev-стенда нет — push в `master` = деплой в прод (`.forgejo/workflows/ci.yml` → self-service).
Делать в ветке, мержить после проверки.

## 4. Файлы (`infra/reid-lab/`)
| Файл | Что |
|---|---|
| `demo_app.py` + `demo.html` | референс-реализация инференса и ранжирования (FastAPI, `http://127.0.0.1:8790`) — **эталон для порта** |
| `export_heads.py` | как получены `export/*` (повторяет обучение из `demo_app.py`) |
| `split.py` | фиксированное честное разбиение (`results/split.json`, `results/trips.json`) |
| `train_eval.py` | подбор на dev-CV + единственная оценка на test |
| `detect.py` | YOLO + ViTPose → кропы, `results/det.jsonl` (боксы, 17 точек, бок для всех 1361 фото) |
| `embed.py`, `embed_local.py`, `embed_aug.py` | эмбеддинги DINOv2 / MiewID / аугментации |
| `results/meta.csv` | id → кличка, дата съёмки, исходный путь в фотобанке |

Подробности экспериментов и грабли — `HANDOFF.md`.

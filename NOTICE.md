# NOTICE

## Портированный алгоритм скоринга и препроцессинга ReID

Часть логики визуального ре-идентифицирования (ReID) в `backend/internal/inference/reid/`
воспроизводит алгоритмы проекта
[zapovednik-public](https://github.com/Oimasi/zapovednik-public)
(хакатонный проект «Особь», команда CUshki). Портировано по договорённости с авторами.

Портированные приёмы:

- **Двухвариантный кроп (raw + masked)** — каждый детект кодируется дважды:
  исходный RGB-кроп и кроп с фоном, залитым серым по сегмент-маске силуэта
  (главный приём против фонового переобучения). Без сегментации — fallback:
  центральный кроп 80% бокса в отдельной версии эмбеддинга
  (`backend/internal/inference/reid/centralcrop.go`, `dualcrop.go`).
- **Скоринг и фьюжн** — агрегация совпадений по кропам личности (max / top-2),
  per-query z-нормировка и взвешенный фьюжн разнородных дескрипторов,
  pair-признаки (сырая оценка, z-оценка, зазор до лучшего) и логистическая
  калибровка на вероятностную шкалу, открытый набор с порогом
  «возможно новая особь» (`isNewSuspected`)
  (`backend/internal/inference/reid/fusion.go`).
- **Side-aware сравнение по оси ракурса** — безразметочное разделение
  левого/правого профиля через разности эмбеддингов кропа и его зеркала
  (`backend/internal/inference/reid/viewpoint.go`).
- **Расширенный фильтр классов детекции** — лошади Пржевальского детектор
  нередко путает с коровой, овцой или зеброй; принимаются COCO-классы
  17, 18, 19, 22 (`backend/internal/inference/yolo/yolo_common.go`).

**Код zapovednik-public не копируется дословно**: в репозитории проекта нет
лицензии, реализация написана с нуля на Go для yc-hack-horses с сохранением
семантики и числовых констант оригинала. Обученные коэффициенты логистической
калибровки и весов фьюжна в zapovednik-public сериализуются в артефактах
развёртывания (head.npz генерируется при обучении), в коде их нет, поэтому в
yc-hack-horses они подаются файлом JSON через `inference.reid_fusion_config`
(структура — `reid.FusionConfig`); пока файл не задан, калибровка выключена и
используется сырой фьюжн-скор.

**Веса CUshki v6** (файнтюненные чекпойнты DINOv2-L, seed 2/3/5,
https://cushki.online/model/cushki-v6-weights.tar) используются с разрешения
авторов; базовая модель DINOv2 — Apache-2.0 (LICENSE-DINOv2.txt).

## CUshki v6 (динамическая размерность, матричная голова)

Веса v6 — ONNX-экспорт файнтюна DINOv2-L (вход `(1,3,448,448)`, выход
`embedding`, dim 2048 = concat(CLS, patch-mean), L2-нормированный;
эквивалентность torch-оригиналу проверена экспортёром: fp32 diff 3.48e-7,
fp16 diff 2.36e-4). Скачивание — `infra/scripts/download-cushki-model.sh`
(pinned sha256; веса не в git). Включение на ноде:

```yaml
inference:
  reid_model_path: /data/models/dinov2_ft_s3.fp16.onnx   # несколько членов ансамбля — через ";"
  reid_model_input_size: 448                              # опционально, дефолт 448
  reid_cushki_config: /data/models/cushki_fusion_config.json  # опционально
  reid_model_dir: /data/models                            # опционально: сегментер (маски/признаки)
```

Файл из `reid_model_path` загружается БЕЗ sha-верификации (файл внешний,
путь задаёт оператор; целостность обеспечивает pin в скрипте скачивания).
Без `reid_model_path` поведение прежнее (megadescriptor, dim 1536) —
безопасный деплой.

**Скоринг CUshki** (`reid/cushki.go`, `reid/cushkiconfig.go`): ранжирование
галереи — максимум косинусной близости по эталонам личности; опциональная
калибровка — мультиномиальная логистическая голова из `fusion_config.json`
(logistic.weights — матрица C×D по классам, softmax(W·x+b)), open-set —
порог `new_threshold` (0.3) на вероятности лучшего класса. `fusion_config.json`
— отдельный формат (`reid.CushkiConfig`), не путать с `reid.FusionConfig`
(head.npz zapovednik: векторные веса над pair-признаками): у CUshki матрица
над признаками, плюс `classes`, `feature_layout` (сборка признака ансамбля:
concat по членам [raw, masked]/sqrt(2), затем /sqrt(M)) и `side_axis`
(ось левого/правого профиля). Вероятности классов присоединяются к
кандидатам (`probability` в API), порядок ранжирования — по вероятности,
только когда каждая личность галереи входит в классы головы; иначе — по
косинусу. Кросс-версии (cushki vs megadescriptor, разные длины векторов)
отвергаются валидацией (`reid.ValidVector`, `reid.Similarity`).

Copyright 2026 Igor Mamaev (pxc1984) — новый код yc-hack-horses.
Алгоритмы: (c) авторы zapovednik-public («Особь», команда CUshki).

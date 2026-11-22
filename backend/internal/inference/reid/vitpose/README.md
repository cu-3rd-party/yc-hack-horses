# internal/inference/reid/vitpose — ViTPose+ (AP-10K) и декод 17 точек

Модуль M3 пайплайна `horseid-dinov2-arcface-v2`: инференс `usyd-community/vitpose-plus-base`
(эксперт AP-10K, `dataset_index=3` запечён при экспорте ONNX модулем M1) и декод 17 ключевых
точек. Координаты — в системе уменьшенного фото (после ресайза до `MaxSide`), индекс COCO
AP-10K. Тип результата — `reid/horseid.KeyPoint`.

## API

```go
est := vitpose.New(runtimeLib, modelDir, modelName) // модель подкладывает модуль M8
kps, err := est.Estimate(ctx, img, box)             // img image.Image, box=[x0,y0,x1,y1]
defer est.Close()
```

`Estimate` лениво открывает сессию onnxruntime (вход `pixel_values`, выход `heatmaps`,
формы `1x3x256x192` и `1x17x64x48`). Без CGO или без файла/библиотеки — `Estimate` возвращает
`inference.ErrModelUnavailable` (graceful-деградация, как в `yolo`).

## Препроцессинг (точный повтор `VitPoseImageProcessor`)

1. Бок `(x,y,w,h)` (COCO) → `center, scale` (`box_to_center_and_scale`, всё во float32):
   - `aspect = 192/256 = 0.75`; если `w > aspect*h`, то `h = w/aspect`, иначе если
     `w < aspect*h`, то `w = h*aspect`;
   - `center = (x + w/2, y + h/2)`, `scale = (w/200, h/200) * 1.25`.
2. Матрица `get_warp_matrix(theta=0, size_input=center*2, size_dst=(192-1, 256-1),
   size_target=scale*200)` (unbiased data processing, Huang et al.):
   - `scale_x = 191 / size_target_x`, `scale_y = 255 / size_target_y`;
   - `M = [[scale_x, 0, scale_x*(0.5*size_target_x - 0.5*size_input_x)],
          [0, scale_y, scale_y*(0.5*size_target_y - 0.5*size_input_y)]]`.
3. `scipy_warp_affine`: `M` дополняется до 3×3, обращается (`inv`), затем компоненты
   переставляются (транспонирование блока 2×2 и обмен сдвигов). Итог отображает координату
   выхода `o=(y_out,x_out)` в координату источника `src = linear·o + offset` — эквивалент
   `cv2.warpAffine`. Для `theta=0` это сводится к
   `src_x = center_x + padded_w*(x_out/191 - 0.5)`, `src_y = center_y + padded_h*(y_out/255 - 0.5)`,
   где `padded = scale*200` (кроп покрывает область `padded`, центр на центре бокса).
4. Билинейная выборка (граница `mode='constant'`: координата вне `[0, side-1]` даёт 0),
   результат квантуется в uint8 (округление «половина вверх», зажим [0,255] — как scipy
   `CASE_INTERP_OUT_UINT`).
5. `rescale` (×1/255) и ImageNet-нормализация `(v - mean)/std` (`mean=.485,.456,.406`,
   `std=.229,.224,.225`), выход NCHW `1x3x256x192` float32.

## Декод (`post_process_pose_estimation`, kernel_size=11)

1. `argmax` каждой тепмапы → целочисленная точка и score (первый максимум при равенстве).
2. DARK-уточнение `post_dark_unbiased_data_processing`: гауссово сглаживание
   (`sigma=0.8`, `radius=5`, граница `reflect`), `clip[0.001,50]`, логарифм, шаг Ньютона по
   обращённому гессиану в окне 3×3. Для симметричного пика почти не смещает точку.
3. `transform_preds`: `scale *= 200`, `scale_x = scale_x/(48-1)`, `scale_y = scale_y/(64-1)`,
   `x = px*scale_x + center_x - scale_x*200/2`, аналогично `y`.

## Проверка точности

Порт сверен с реальным `transformers` (venv модуля M1, `VitPoseImageProcessor` /
`TorchvisionBackend`, transformers 5.24.0) и с реальными тепмапами модели
`usyd-community/vitpose-plus-base`:

- препроцессинг: расхождение ≤ 1 ULP float32 (`2.4e-7`), cos = 1.000000000 на 5 боксах
  (в т.ч. с выходом кропа за границы фото);
- декод: `max|Δxy| ≤ 3.1e-5` px, score совпадает точно.

Ключевая деталь: `center`, `scale` и матрица считаются во **float32** (как numpy-массивы
transformers) — вычисление во float64 даёт ошибку ~6e-6 в сдвиге и до 1 LSB на части
граничных пикселей. Тесты: геометрия (`geometry_test.go`), препроцессинг на golden-значениях
реального процессора (`preprocess_test.go`), декод на синтетических тепмапах и golden-точках
реального `post_process_pose_estimation` (`decode_test.go`). Тесты не запускают ONNX-сессию
(CI без модели); `validate_ref_test.go` — дополнительный golden-прогон против реального
`transformers`, включается переменной `VITPOSE_REF_DIR` (по умолчанию пропускается).

## Контракт с M1/M8

- Имена тензоров: вход `pixel_values` (динамический batch), выход `heatmaps` (`1x17x64x48`).
- Бок `Estimate` — `[x0,y0,x1,y1]`; внутри переводится в COCO для аффинного преобразования.

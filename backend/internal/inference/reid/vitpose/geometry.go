// Package vitpose: инференс ViTPose+ base (эксперт AP-10K, dataset_index=3) и декод
// 17 ключевых точек. Точный повтор препроцессинга VitPoseImageProcessor из transformers
// (аффинное преобразование бокса, кроп 256x192, ImageNet-нормализация) и декода
// post_process_pose_estimation (argmax тепмап + DARK-уточнение + обратный аффинный перенос).
//
// Модуль M3 пайплайна horseid-dinov2-arcface-v2. Контракт типов — reid/horseid.
// ONNX-сессия собирается только под cgo (onnxruntime_go); без cgo пакет компилируется,
// но Estimate деградирует в inference.ErrModelUnavailable.
package vitpose

import "math"

// Параметры входа ViTPose+ base и формы тепмап (input/4).
const (
	// InputWidth, InputHeight — стороны кропа (W, H) на входе модели.
	InputWidth  = 192
	InputHeight = 256
	// NumKeypoints — число точек (индекс COCO AP-10K).
	NumKeypoints = 17
	// HeatmapWidth, HeatmapHeight — стороны выхода (тепмапы) при таком входе.
	HeatmapWidth  = InputWidth / 4
	HeatmapHeight = InputHeight / 4
	// normalizeFactor и paddingFactor — из box_to_center_and_scale (transformers).
	normalizeFactor = 200.0
	paddingFactor   = 1.25
)

// imagenetMean/Std — нормализация входа (RGB), как в preprocessor_config.json.
// Хранятся во float32: numpy-массивы mean/std у transformers имеют тип float32, а при
// вычитании с float64-изображением повышаются до float64 (это влияет на последний бит).
var (
	imagenetMean = [3]float32{0.485, 0.456, 0.406}
	imagenetStd  = [3]float32{0.229, 0.224, 0.225}
)

// rescaleFactor = 1/255, как rescale_factor в preprocessor_config.json.
const rescaleFactor = 1.0 / 255.0

// boxToCenterAndScale повторяет transformers.models.vitpose.box_to_center_and_scale.
// Бок задаётся в COCO (x, y, w, h). Стороны image_width/image_height берутся из размера
// входа модели (192x256), поэтому бок расширяется до соотношения 192:256 и масштабируется
// с запасом padding_factor. Значение normalize_factor=200 задаёт «пиксельный» масштаб.
// center и scale возвращаются во float32 — как numpy-массивы в transformers (это важно
// для битового совпадения аффинного преобразования).
func boxToCenterAndScale(x, y, w, h float64) (center, scale [2]float32) {
	aspectRatio := float64(InputWidth) / float64(InputHeight)
	cx, cy := x+w*0.5, y+h*0.5
	if w > aspectRatio*h {
		h = w / aspectRatio
	} else if w < aspectRatio*h {
		w = h * aspectRatio
	}
	center = [2]float32{float32(cx), float32(cy)}
	scale = [2]float32{
		float32(w/normalizeFactor) * paddingFactor,
		float32(h/normalizeFactor) * paddingFactor,
	}
	return center, scale
}

// warpMatrix — точный порт transformers.models.vitpose.get_warp_matrix
// (Huang et al., unbiased data processing). theta=0 всегда, но сохраняем общую форму.
// sizeInput и sizeTarget — float32 (numpy-массивы float32), sizeDst — float64
// (np.array(size)-1.0); внутренняя сумма считается во float32, как numpy.
func warpMatrix(thetaDeg float64, sizeInput, sizeTarget [2]float32, sizeDst [2]float64) [2][3]float32 {
	theta := thetaDeg * math.Pi / 180.0
	cosT, sinT := math.Cos(theta), math.Sin(theta)
	scaleX := sizeDst[0] / float64(sizeTarget[0])
	scaleY := sizeDst[1] / float64(sizeTarget[1])
	innerX := float32(-0.5)*sizeInput[0]*float32(cosT) + float32(0.5)*sizeInput[1]*float32(sinT) + float32(0.5)*sizeTarget[0]
	innerY := float32(-0.5)*sizeInput[0]*float32(sinT) - float32(0.5)*sizeInput[1]*float32(cosT) + float32(0.5)*sizeTarget[1]
	var m [2][3]float32
	m[0][0] = float32(cosT * scaleX)
	m[0][1] = float32(-sinT * scaleX)
	m[0][2] = float32(scaleX * float64(innerX))
	m[1][0] = float32(sinT * scaleY)
	m[1][1] = float32(cosT * scaleY)
	m[1][2] = float32(scaleY * float64(innerY))
	return m
}

// inverse3x3 — обратная матрица 3x3 (метод алгебраических дополнений) в float64.
func inverse3x3(m [3][3]float64) [3][3]float64 {
	a, b, c := m[0][0], m[0][1], m[0][2]
	d, e, f := m[1][0], m[1][1], m[1][2]
	g, h, i := m[2][0], m[2][1], m[2][2]
	det := a*(e*i-f*h) - b*(d*i-f*g) + c*(d*h-e*g)
	if det == 0 {
		return m
	}
	invDet := 1.0 / det
	return [3][3]float64{
		{(e*i - f*h) * invDet, (c*h - b*i) * invDet, (b*f - c*e) * invDet},
		{(f*g - d*i) * invDet, (a*i - c*g) * invDet, (c*d - a*f) * invDet},
		{(d*h - e*g) * invDet, (b*g - a*h) * invDet, (a*e - b*d) * invDet},
	}
}

// warpPullMatrix повторяет scipy_warp_affine из transformers: строит 2x3-матрицу
// get_warp_matrix, дополняет её до 3x3, обращает и переставляет компоненты
// (транспонирование блока 2x2 и обмен сдвигов). Результат отображает координату выхода
// o=(y,x) в координату источника: src = linear*o + offset (соглашение scipy.ndimage
// affine_transform «pull»). Для theta=0 линейная часть диагональна.
func warpPullMatrix(box [4]float64) (linear [2][2]float64, offset [2]float64) {
	center, scale := boxToCenterAndScale(box[0], box[1], box[2]-box[0], box[3]-box[1])
	m := warpMatrix(0,
		[2]float32{center[0] * 2, center[1] * 2},
		[2]float32{scale[0] * normalizeFactor, scale[1] * normalizeFactor},
		[2]float64{InputWidth - 1, InputHeight - 1},
	)
	m3 := [3][3]float64{
		{float64(m[0][0]), float64(m[0][1]), float64(m[0][2])},
		{float64(m[1][0]), float64(m[1][1]), float64(m[1][2])},
		{0, 0, 1},
	}
	inv := inverse3x3(m3)
	linear = [2][2]float64{{inv[1][1], inv[1][0]}, {inv[0][1], inv[0][0]}}
	offset = [2]float64{inv[1][2], inv[0][2]}
	return linear, offset
}

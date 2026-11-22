// crop.go — модуль M4 «кропы и геометрия головы».
//
// Прямой порт чистой геометрии из analyse() в infra/reid-lab/demo_app.py (строки ~177-210),
// без ONNX и без моделей: построение head-бокса по 17 точкам ViTPose, кропы тела и головы с
// обрезкой по кадру, вычисление ракурса (View). EXIF-поворот и ресайз до MaxSide сюда не
// входят — изображение приходит уже повёрнутым и уменьшенным.

package horseid

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"math"
)

// ErrEmptyCrop — бокс после обрезки по кадру не содержит пикселей.
var ErrEmptyCrop = errors.New("horseid: empty crop")

// headPointIdx — индексы точек головы в COCO AP-10K: 0 нос, 1-2 глаза, 3-4 уши.
var headPointIdx = [...]int{0, 1, 2, 3, 4}

// hipPointIdx — индексы бёдер в COCO AP-10K.
var hipPointIdx = [...]int{11, 12}

// ClipBox обрезает бокс по границам кадра w×h — аналог clip_box() из demo_app.py.
func ClipBox(b [4]float64, w, h int) [4]float64 {
	fw, fh := float64(w), float64(h)
	return [4]float64{
		math.Max(0, b[0]),
		math.Max(0, b[1]),
		math.Min(fw, b[2]),
		math.Min(fh, b[3]),
	}
}

// HeadBox строит квадратный head-бокс по 17 ключевым точкам ViTPose.
//
// Берутся точки 0 (нос), 1-2 (глаза) и 3-4 (уши) со score > KpScoreMin. Для головы нужен
// нос И хотя бы один глаз И не менее 3 отобранных точек. Центр квадрата — центр bbox
// отобранных точек, сторона = HeadSideScale × max(ширина, высота) bbox. Если сторона меньше
// HeadSideMin пикселей — головы нет (nil). Результат обрезается по кадру w×h.
func HeadBox(kps []KeyPoint, w, h int) *[4]float64 {
	if len(kps) < len(headPointIdx) {
		return nil
	}
	var ok [5]bool
	found := 0
	for _, i := range headPointIdx {
		if kps[i].Score > KpScoreMin {
			ok[i] = true
			found++
		}
	}
	// нужен нос и хотя бы один глаз, всего не меньше 3 точек
	if !ok[0] || !(ok[1] || ok[2]) || found < 3 {
		return nil
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, i := range headPointIdx {
		if !ok[i] {
			continue
		}
		minX = math.Min(minX, kps[i].X)
		minY = math.Min(minY, kps[i].Y)
		maxX = math.Max(maxX, kps[i].X)
		maxY = math.Max(maxY, kps[i].Y)
	}
	side := math.Max(maxX-minX, maxY-minY) * HeadSideScale
	if side < HeadSideMin {
		return nil
	}
	cx, cy := (minX+maxX)/2, (minY+maxY)/2
	box := ClipBox([4]float64{cx - side/2, cy - side/2, cx + side/2, cy + side/2}, w, h)
	return &box
}

// BodyBox расширяет бокс тела на BodyMargin пикселей со всех сторон и обрезает по кадру w×h.
func BodyBox(body [4]float64, w, h int) [4]float64 {
	return ClipBox([4]float64{
		body[0] - BodyMargin,
		body[1] - BodyMargin,
		body[2] + BodyMargin,
		body[3] + BodyMargin,
	}, w, h)
}

// ComputeView определяет ракурс лошади по ключевым точкам и боксу тела (demo_app.py).
//
// Нос (точка 0) левее центра бёдер (11,12) более чем на ViewSideThresh·ширину бокса → left,
// правее → right, иначе front. Носа нет, но есть бёдра → rear. Иначе unknown.
func ComputeView(kps []KeyPoint, body [4]float64) View {
	nose := len(kps) > 0 && kps[0].Score > KpScoreMin
	var hipSum float64
	var hips int
	for _, i := range hipPointIdx {
		if i < len(kps) && kps[i].Score > KpScoreMin {
			hipSum += kps[i].X
			hips++
		}
	}
	switch {
	case nose && hips > 0:
		width := body[2] - body[0]
		if width <= 0 {
			return ViewFront
		}
		dx := (kps[0].X - hipSum/float64(hips)) / width
		if dx < -ViewSideThresh {
			return ViewLeft
		}
		if dx > ViewSideThresh {
			return ViewRight
		}
		return ViewFront
	case !nose && hips > 0:
		return ViewRear
	default:
		return ViewUnknown
	}
}

// cropRect переводит вещественный бокс в целочисленный прямоугольник пикселей. Повторяет
// map(int, box) из demo_app.py (усечение к нулю) и обрезку по границам кадра. Вырожденный
// прямоугольник возвращается пустым.
func cropRect(box [4]float64, bounds image.Rectangle) image.Rectangle {
	cb := ClipBox(box, bounds.Dx(), bounds.Dy())
	// Вырожденный/перевёрнутый бокс: image.Rect нормализует Min/Max, поэтому пустоту
	// проверяем по исходным координатам, как пустой кроп в PIL.
	if cb[2] <= cb[0] || cb[3] <= cb[1] {
		return image.Rectangle{}
	}
	r := image.Rect(int(cb[0]), int(cb[1]), int(cb[2]), int(cb[3]))
	if r.Empty() { // после усечения дробных координат
		return image.Rectangle{}
	}
	return r.Add(bounds.Min).Intersect(bounds)
}

// CropPNG вырезает из img область box (в координатах кадра) и кодирует её в PNG. Бокс
// обрезается по кадру; пустая область даёт ErrEmptyCrop.
func CropPNG(img image.Image, box [4]float64) ([]byte, error) {
	r := cropRect(box, img.Bounds())
	if r.Empty() {
		return nil, ErrEmptyCrop
	}
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), img, r.Min, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BodyCropPNG вырезает body-кроп: бокс тела с запасом BodyMargin, обрезанный по кадру.
func BodyCropPNG(img image.Image, body [4]float64) ([]byte, error) {
	b := img.Bounds()
	return CropPNG(img, BodyBox(body, b.Dx(), b.Dy()))
}

// HeadCropPNG вырезает head-кроп по готовому head-боксу (обрезается по кадру).
func HeadCropPNG(img image.Image, head [4]float64) ([]byte, error) {
	return CropPNG(img, head)
}

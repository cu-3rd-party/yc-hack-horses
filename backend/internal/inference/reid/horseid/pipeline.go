// Оркестратор ReID-пайплайна horseid (модуль M9, см. HANDOFF_GO.md).
//
// Полный путь: байты фото → EXIF-ориентация и ресайз до MaxSide по длинной
// стороне → самый большой бокс лошади (YOLO, класс 17 horse) и поза (ViTPose+)
// → body/head-кропы (модуль M4) → эмбеддинги ArcFace. Реализует контракты
// Detector, Embedder и Pipeline из contract.go поверх переданных снаружи
// зависимостей; реальные конструкторы детектора боксов, оценщика позы и
// эмбеддера допишет координатор на мёрже.
package horseid

import (
	"bytes"
	"fmt"
	"image"
	"math"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/disintegration/imaging"

	_ "golang.org/x/image/webp" // декодирование WebP-фото на входе пайплайна
)

// Box — бокс лошади (x0,y0,x1,y1) в координатах уменьшенного фото вместе с
// уверенностью детектора. Детектор боксов YOLO отдаёт список таких боксов.
type Box struct {
	Box        [4]float64
	Confidence float64
}

// BoxDetector — детектор боксов YOLO11s (COCO, класс 17 horse) по уменьшенному
// фото. Реализацию поверх ONNX-сессии собирает координатор.
type BoxDetector interface {
	DetectHorses(img image.Image) ([]Box, error)
}

// PoseEstimator — оценщик позы ViTPose+ base (AP-10K, dataset_index=3):
// 17 ключевых точек COCO AP-10K по уменьшенному фото и боксу лошади.
type PoseEstimator interface {
	EstimatePose(img image.Image, box [4]float64) ([]KeyPoint, error)
}

// Result — результат полного прогона пайплайна: детекция, эмбеддинги и
// уверенность детектора по выбранному боксу (её ждёт адаптер comparison).
type Result struct {
	Detection  *Detection
	Embedding  Embedding
	Confidence float64
}

// horsePipeline реализует Pipeline: детектор боксов, оценщик позы и эмбеддер.
type horsePipeline struct {
	boxes BoxDetector
	pose  PoseEstimator
	embed Embedder
}

// Компиляторные проверки реализации контрактов скелета.
var (
	_ Detector = (*horsePipeline)(nil)
	_ Embedder = (*horsePipeline)(nil)
	_ Pipeline = (*horsePipeline)(nil)
)

// New собирает пайплайн из переданных снаружи зависимостей.
func New(boxes BoxDetector, pose PoseEstimator, embed Embedder) Pipeline {
	return &horsePipeline{boxes: boxes, pose: pose, embed: embed}
}

// PreprocessImage применяет EXIF-ориентацию и уменьшает фото до MaxSide по
// длинной стороне (аналог PIL ImageOps.exif_transpose + thumbnail((1280,1280))).
// Уменьшение бикубическое (PIL thumbnail по умолчанию BICUBIC ≈ CatmullRom);
// апскейла нет.
func PreprocessImage(data []byte) (image.Image, error) {
	img, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", inference.ErrInvalidImage, err)
	}
	return imaging.Thumbnail(img, MaxSide, MaxSide, imaging.CatmullRom), nil
}

// Detect реализует Detector: препроцесс входных байтов и детекция бокса с позой.
func (p *horsePipeline) Detect(data []byte) (*Detection, error) {
	img, err := PreprocessImage(data)
	if err != nil {
		return nil, err
	}
	det, _, err := p.detect(img)
	return det, err
}

// EmbedHead реализует Embedder: делегирует эмбеддеру.
func (p *horsePipeline) EmbedHead(crop []byte) ([]float64, error) { return p.embed.EmbedHead(crop) }

// EmbedBody реализует Embedder: делегирует эмбеддеру.
func (p *horsePipeline) EmbedBody(crop []byte) ([]float64, error) { return p.embed.EmbedBody(crop) }

// Analyze проходит полный путь по байтам фото: препроцесс, детекция, кропы
// (модуль M4) и эмбеддинги. Возвращает inference.ErrNoAnimalDetected, если
// лошадь не найдена. Если голову найти не удалось, HasHead = false.
func (p *horsePipeline) Analyze(data []byte) (Result, error) {
	img, err := PreprocessImage(data)
	if err != nil {
		return Result{}, err
	}
	det, confidence, err := p.detect(img)
	if err != nil {
		return Result{}, err
	}
	emb, err := p.embedCrops(img, det)
	if err != nil {
		return Result{}, err
	}
	return Result{Detection: det, Embedding: emb, Confidence: confidence}, nil
}

// detect находит самый большой бокс лошади, оценивает позу и собирает
// Detection с головным боксом и ракурсом. Уверенность отдаётся отдельно:
// в контрактном Detection поля для неё нет.
func (p *horsePipeline) detect(img image.Image) (*Detection, float64, error) {
	boxes, err := p.boxes.DetectHorses(img)
	if err != nil {
		return nil, 0, err
	}
	if len(boxes) == 0 {
		return nil, 0, inference.ErrNoAnimalDetected
	}
	best := boxes[0]
	for _, b := range boxes[1:] {
		if boxArea(b.Box) > boxArea(best.Box) {
			best = b
		}
	}
	kps, err := p.pose.EstimatePose(img, best.Box)
	if err != nil {
		return nil, 0, err
	}
	b := img.Bounds()
	det := &Detection{
		BodyBox:   best.Box,
		Keypoints: kps,
		View:      classifyView(kps, best.Box),
		NumHorses: len(boxes),
	}
	det.HeadBox = buildHeadBox(kps, b.Dx(), b.Dy())
	return det, best.Confidence, nil
}

// embedCrops считает эмбеддинги body- и head-кропов. Body всегда присутствует;
// head — только если buildHeadBox нашёл квадрат головы.
func (p *horsePipeline) embedCrops(img image.Image, det *Detection) (Embedding, error) {
	var emb Embedding
	bodyCrop, err := CropBody(img, det)
	if err != nil {
		return Embedding{}, err
	}
	if emb.Body, err = p.embed.EmbedBody(bodyCrop); err != nil {
		return Embedding{}, err
	}
	emb.HasBody = true
	if det.HeadBox != nil {
		headCrop, err := CropHead(img, det)
		if err != nil {
			return Embedding{}, err
		}
		if emb.Head, err = p.embed.EmbedHead(headCrop); err != nil {
			return Embedding{}, err
		}
		emb.HasHead = true
	}
	return emb, nil
}

// concatVector собирает вектор дескриптора: сначала head256, затем body256.
// Отсутствующая часть пропускается (HasHead/HasBody), длина = 512 или 256.
func concatVector(emb Embedding) []float64 {
	switch {
	case emb.HasHead && emb.HasBody:
		v := make([]float64, 0, 2*EmbedDim)
		v = append(v, emb.Head...)
		v = append(v, emb.Body...)
		return v
	case emb.HasHead:
		return append([]float64(nil), emb.Head...)
	case emb.HasBody:
		return append([]float64(nil), emb.Body...)
	default:
		return nil
	}
}

// buildHeadBox строит квадратный head-бокс по точкам {0 нос, 1,2 глаза, 3,4 уши}
// со score > KpScoreMin: нужны нос, хотя бы один глаз и ≥3 точки; сторона =
// HeadSideScale × max(ширина,высота) bbox точек, минимум HeadSideMin; полученный
// бокс обрезается по кадру. Логика повторяет demo_app.analyse.
func buildHeadBox(kps []KeyPoint, w, h int) *[4]float64 {
	ok := func(i int) bool { return i < len(kps) && kps[i].Score > KpScoreMin }
	if !ok(0) || !(ok(1) || ok(2)) {
		return nil
	}
	var xs, ys []float64
	for i := range min(5, len(kps)) {
		if ok(i) {
			xs = append(xs, kps[i].X)
			ys = append(ys, kps[i].Y)
		}
	}
	if len(xs) < 3 {
		return nil
	}
	minX, maxX := minMax(xs)
	minY, maxY := minMax(ys)
	cx, cy := (minX+maxX)/2, (minY+maxY)/2
	side := math.Max(maxX-minX, maxY-minY) * HeadSideScale
	if side < HeadSideMin {
		return nil
	}
	box := [4]float64{cx - side/2, cy - side/2, cx + side/2, cy + side/2}
	clipBox(&box, w, h)
	return &box
}

// classifyView определяет ракурс по смещению носа относительно центра бёдер
// (точки 11,12). Ракурс нужен только для UI, в ранжировании не участвует.
func classifyView(kps []KeyPoint, box [4]float64) View {
	ok := func(i int) bool { return i < len(kps) && kps[i].Score > KpScoreMin }
	hips, sumX := 0, 0.0
	for _, i := range []int{11, 12} {
		if ok(i) {
			sumX += kps[i].X
			hips++
		}
	}
	width := box[2] - box[0]
	if ok(0) && hips > 0 && width > 0 {
		dx := (kps[0].X - sumX/float64(hips)) / width
		switch {
		case dx < -ViewSideThresh:
			return ViewLeft
		case dx > ViewSideThresh:
			return ViewRight
		default:
			return ViewFront
		}
	}
	if !ok(0) && hips > 0 {
		return ViewRear
	}
	return ViewUnknown
}

// boxArea — площадь бокса [x0,y0,x1,y1].
func boxArea(b [4]float64) float64 { return (b[2] - b[0]) * (b[3] - b[1]) }

// clipBox обрезает бокс по кадру [0,w]×[0,h].
func clipBox(box *[4]float64, w, h int) {
	box[0] = min(max(box[0], 0), float64(w))
	box[1] = min(max(box[1], 0), float64(h))
	box[2] = min(max(box[2], 0), float64(w))
	box[3] = min(max(box[3], 0), float64(h))
}

// minMax возвращает минимум и максимум среза; срез непустой по контракту вызова.
func minMax(v []float64) (lo, hi float64) {
	lo, hi = v[0], v[0]
	for _, x := range v[1:] {
		lo, hi = min(lo, x), max(hi, x)
	}
	return lo, hi
}

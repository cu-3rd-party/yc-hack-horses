// Адаптер horseid-пайплайна к comparison (модуль M9) и скелет кропов M4.
//
// HorseAnalyzer реализует comparison.Analyzer: один Prediction на найденную
// лошадь с вектором concat(head256, body256). CropBody/CropHead — тонкие
// заглушки, которые на мёрже заменяются реализацией модуля M4 (crop.go).
package horseid

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"math"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
)

// HorseAnalyzer реализует comparison.Analyzer поверх horseid-пайплайна.
type HorseAnalyzer struct {
	pipeline pipelineRunner
}

// pipelineRunner — зависимость адаптера: полный путь фото до Result.
type pipelineRunner interface {
	Analyze(data []byte) (Result, error)
}

// Компиляторная проверка совместимости с comparison.Analyzer.
var _ comparison.Analyzer = HorseAnalyzer{}

// NewHorseAnalyzer собирает адаптер comparison поверх пайплайна из трёх
// зависимостей: детектор боксов YOLO, оценщик позы ViTPose, эмбеддер ArcFace.
func NewHorseAnalyzer(boxes BoxDetector, pose PoseEstimator, embed Embedder) HorseAnalyzer {
	return HorseAnalyzer{pipeline: &horsePipeline{boxes: boxes, pose: pose, embed: embed}}
}

// Analyze реализует comparison.Analyzer. Контекст и mime не используются:
// пайплайн работает локально по байтам изображения. Если лошадь не найдена,
// возвращается inference.ErrNoAnimalDetected (существующая семантика).
func (a HorseAnalyzer) Analyze(_ context.Context, imageBytes []byte, _ string) ([]comparison.Prediction, error) {
	res, err := a.pipeline.Analyze(imageBytes)
	if err != nil {
		return nil, err
	}
	return []comparison.Prediction{{
		Confidence: res.Confidence,
		Descriptor: comparison.Descriptor{Version: Version, Vector: concatVector(res.Embedding)},
	}}, nil
}

// CropBody возвращает body-кроп: самый большой бокс лошади с запасом BodyMargin,
// обрезанный по кадру, как PNG-байты для эмбеддера.
//
// СКЕЛЕТ, ЗАМЕНЯЕТСЯ РЕАЛИЗАЦИЕЙ МОДУЛЯ M4 (файл crop.go): тонкая заглушка
// вырезает прямоугольник и кодирует его в PNG, чтобы пакет собирался. На мёрже
// координатор удаляет CropBody/CropHead отсюда и подключает настоящий crop.go с
// теми же сигнатурами: (img image.Image, det *Detection) ([]byte, error).
func CropBody(img image.Image, det *Detection) ([]byte, error) {
	box := [4]float64{
		det.BodyBox[0] - BodyMargin,
		det.BodyBox[1] - BodyMargin,
		det.BodyBox[2] + BodyMargin,
		det.BodyBox[3] + BodyMargin,
	}
	return encodeCrop(img, box)
}

// CropHead возвращает head-кроп из det.HeadBox, обрезанный по кадру, как
// PNG-байты. Требует det.HeadBox != nil (проверяет вызывающий).
//
// СКЕЛЕТ, ЗАМЕНЯЕТСЯ РЕАЛИЗАЦИЕЙ МОДУЛЯ M4 (файл crop.go): см. CropBody.
func CropHead(img image.Image, det *Detection) ([]byte, error) {
	if det.HeadBox == nil {
		return nil, nil
	}
	return encodeCrop(img, *det.HeadBox)
}

// encodeCrop вырезает бокс из изображения (координаты обрезаются по кадру и
// усекаются как int(...) в demo_app) и кодирует результат в PNG.
func encodeCrop(img image.Image, box [4]float64) ([]byte, error) {
	b := img.Bounds()
	clipBox(&box, b.Dx(), b.Dy())
	r := image.Rect(
		int(math.Floor(box[0]))+b.Min.X,
		int(math.Floor(box[1]))+b.Min.Y,
		int(math.Floor(box[2]))+b.Min.X,
		int(math.Floor(box[3]))+b.Min.Y,
	)
	if r.Empty() {
		r = image.Rect(b.Min.X, b.Min.Y, b.Min.X+1, b.Min.Y+1)
	}
	sub := img
	if s, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		sub = s.SubImage(r)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, sub); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

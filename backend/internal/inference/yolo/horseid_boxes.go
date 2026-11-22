//go:build cgo

// Адаптер YOLO-движка к интерфейсу horseid.BoxDetector: боксы класса 17
// (horse) в координатах переданного изображения.
package yolo

import (
	"fmt"
	"image"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid/horseid"
)

// DetectHorses реализует horseid.BoxDetector: YOLO-детекция + NMS, самый
// большой бокс получает пайплайн сам (по площади), отдаём все боксы.
func (e *Engine) DetectHorses(img image.Image) ([]horseid.Box, error) {
	e.life.RLock()
	defer e.life.RUnlock()
	if err := e.ensureSession(); err != nil {
		return nil, err
	}
	out, params, err := e.runInference(img)
	if err != nil {
		return nil, err
	}
	dets := nms(decodeOutput(out, horseClassID, e.cfg.ConfThreshold), e.cfg.IouThreshold)
	if len(dets) == 0 {
		return nil, fmt.Errorf("%w: class %d, conf %.2f", inference.ErrNoAnimalDetected, horseClassID, e.cfg.ConfThreshold)
	}
	boxes := make([]horseid.Box, 0, len(dets))
	for _, det := range dets {
		box := unletterbox(det.Box, params)
		if box.Empty() {
			continue
		}
		boxes = append(boxes, horseid.Box{
			Box:        [4]float64{float64(box.Min.X), float64(box.Min.Y), float64(box.Max.X), float64(box.Max.Y)},
			Confidence: det.Confidence,
		})
	}
	if len(boxes) == 0 {
		return nil, fmt.Errorf("%w: класс %d не найден", inference.ErrNoAnimalDetected, horseClassID)
	}
	return boxes, nil
}

package yolo

import (
	"image"
	"sort"
)

// Detection — одна детекция YOLO в координатах входного тензора.
type Detection struct {
	Box        box
	Confidence float64
	ClassID    int
}

// box — прямоугольник в координатах тензора (целые, без image.Rectangle
// для независимости от порядка Min/Max).
type box struct {
	x0, y0, x1, y1 int
}

func (b box) rect() image.Rectangle {
	return image.Rect(b.x0, b.y0, b.x1, b.y1)
}

func (b box) area() int {
	w := b.x1 - b.x0
	if w < 0 {
		w = 0
	}
	h := b.y1 - b.y0
	if h < 0 {
		h = 0
	}
	return w * h
}

func intersect(a, b box) box {
	x0, y0 := max(a.x0, b.x0), max(a.y0, b.y0)
	x1, y1 := min(a.x1, b.x1), min(a.y1, b.y1)
	return box{x0: x0, y0: y0, x1: x1, y1: y1}
}

// decodeOutput разбирает выход YOLOv8 (1x84x8400): для каждого анкора
// cx,cy,w,h (пиксели входа) и оценки классов. Возвращает детекции класса
// classID с уверенностью >= confThreshold, отсортированные по убыванию.
func decodeOutput(data []float32, classID int, confThreshold float64) []Detection {
	if len(data) < numChannels*numAnchors {
		return nil
	}
	plane := numAnchors
	scoreBase := (4 + classID) * plane
	var out []Detection
	for i := 0; i < numAnchors; i++ {
		conf := float64(data[scoreBase+i])
		if conf < confThreshold {
			continue
		}
		cx := float64(data[i])
		cy := float64(data[plane+i])
		w := float64(data[2*plane+i])
		h := float64(data[3*plane+i])
		b := box{
			x0: int(cx - w/2),
			y0: int(cy - h/2),
			x1: int(cx + w/2),
			y1: int(cy + h/2),
		}
		out = append(out, Detection{Box: b, Confidence: conf, ClassID: classID})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	return out
}

// YOLO26 emits xyxy, score, class. Keep overlapping horses, but collapse identical boxes.
func decodeEndToEnd(data []float32, classID int, confThreshold float64) []Detection {
	var out []Detection
	for len(data) >= 6 {
		row := data[:6]
		data = data[6:]
		if row[5] == float32(classID) && float64(row[4]) >= confThreshold {
			out = append(out, Detection{
				Box:        box{int(row[0]), int(row[1]), int(row[2]), int(row[3])},
				Confidence: float64(row[4]), ClassID: classID,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	seen := make(map[box]bool, len(out))
	unique := out[:0]
	for _, d := range out {
		if !seen[d.Box] {
			unique = append(unique, d)
			seen[d.Box] = true
		}
	}
	return unique
}

// nms — non-maximum suppression: убирает пересекающиеся боксы с IoU > threshold.
// Вход уже отсортирован по убыванию уверенности.
func nms(dets []Detection, iouThreshold float64) []Detection {
	kept := make([]Detection, 0, len(dets))
	for _, d := range dets {
		suppressed := false
		for _, k := range kept {
			if iou(d.Box, k.Box) > iouThreshold {
				suppressed = true
				break
			}
		}
		if !suppressed {
			kept = append(kept, d)
		}
	}
	return kept
}

// iou — intersection over union двух боксов.
func iou(a, b box) float64 {
	inter := intersect(a, b).area()
	union := a.area() + b.area() - inter
	if union <= 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

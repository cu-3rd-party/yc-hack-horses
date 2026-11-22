package yolo

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeEndToEndOverlappingHorses(t *testing.T) {
	rows := []float32{
		12, 20, 80, 100, .60, 17,
		10, 22, 82, 98, .91, 17,
		10, 22, 82, 98, .45, 17,
		15, 18, 80, 97, .99, 16,
		15, 18, 80, 97, .10, 17,
		0, 0, 0, 0, 0, 0,
	}
	dets := decodeEndToEnd(rows, horseClassID, .25)
	require.Len(t, dets, 2)
	require.Equal(t, box{10, 22, 82, 98}, dets[0].Box)
	require.Equal(t, box{12, 20, 80, 100}, dets[1].Box)
}

// TestDecodeOutputFiltersAndSorts — декодер берёт только класс horse и
// сортирует по убыванию уверенности.
func TestDecodeOutputFiltersAndSorts(t *testing.T) {
	data := make([]float32, numChannels*numAnchors)
	set := func(anchor int, cx, cy, w, h, conf float32) {
		data[anchor] = cx
		data[numAnchors+anchor] = cy
		data[2*numAnchors+anchor] = w
		data[3*numAnchors+anchor] = h
		data[(4+horseClassID)*numAnchors+anchor] = conf
	}
	set(0, 320, 320, 100, 200, 0.9)
	set(1, 100, 100, 50, 50, 0.3)
	set(2, 500, 500, 50, 50, 0.5)
	// чужой класс — должен быть отфильтрован
	data[(4+16)*numAnchors+3] = 0.99

	dets := decodeOutput(data, horseClassID, 0.25)
	require.Len(t, dets, 3)
	require.InDelta(t, 0.9, dets[0].Confidence, 1e-6)
	require.InDelta(t, 0.5, dets[1].Confidence, 1e-6)
	require.InDelta(t, 0.3, dets[2].Confidence, 1e-6)
}

// TestDecodeOutputBelowThreshold — уверенность ниже порога отбрасывается.
func TestDecodeOutputBelowThreshold(t *testing.T) {
	data := make([]float32, numChannels*numAnchors)
	data[(4+horseClassID)*numAnchors] = 0.1
	require.Empty(t, decodeOutput(data, horseClassID, 0.25))
}

// TestNMS — пересекающиеся боксы подавляются, разные остаются.
func TestNMS(t *testing.T) {
	dets := []Detection{
		{Box: box{0, 0, 100, 200}, Confidence: 0.9},
		{Box: box{10, 10, 110, 210}, Confidence: 0.8}, // IoU ~0.68 > 0.45
		{Box: box{300, 300, 400, 500}, Confidence: 0.7},
	}
	kept := nms(dets, 0.45)
	require.Len(t, kept, 2)
	require.Equal(t, 0.9, kept[0].Confidence)
	require.Equal(t, 0.7, kept[1].Confidence)
}

// TestNMSDistinctBoxes — непересекающиеся боксы все остаются.
func TestNMSDistinctBoxes(t *testing.T) {
	dets := []Detection{
		{Box: box{0, 0, 10, 10}, Confidence: 0.5},
		{Box: box{100, 100, 110, 110}, Confidence: 0.4},
	}
	require.Len(t, nms(dets, 0.45), 2)
}

// TestIOU — вычисление IoU на известных прямоугольниках.
func TestIOU(t *testing.T) {
	a := box{0, 0, 10, 10}
	require.InDelta(t, 1.0, iou(a, a), 1e-9)
	require.Equal(t, 0.0, iou(a, box{20, 20, 30, 30}))
	require.InDelta(t, 0.5, iou(a, box{0, 0, 10, 20}), 1e-9) // 100/200
}

// TestUnletterbox — обратный маппинг координат: бокс в тензоре возвращается
// в исходные координаты изображения.
func TestUnletterbox(t *testing.T) {
	// изображение 320x640 → letterbox 640x640: scale=0.5, padX=0, padY=160
	p := letterboxParams{scale: 0.5, padX: 0, padY: 160, srcRect: rect(0, 0, 320, 640)}
	b := box{x0: 0, y0: 160, x1: 320, y1: 480}
	got := unletterbox(b, p)
	require.Equal(t, 0, got.Min.X)
	require.Equal(t, 0, got.Min.Y)
	require.Equal(t, 320, got.Max.X)
	require.Equal(t, 640, got.Max.Y)
}

// TestUnletterboxClamps — координаты за границами обрезаются до изображения.
func TestUnletterboxClamps(t *testing.T) {
	p := letterboxParams{scale: 1, padX: 0, padY: 0, srcRect: rect(0, 0, 100, 100)}
	got := unletterbox(box{-10, -10, 500, 500}, p)
	require.Equal(t, rect(0, 0, 100, 100), got)
}

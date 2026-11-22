package comparison

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid/horseid"
)

// fakeAnalyzerBoxes — детектор боксов: отдаёт заранее заданный список.
type fakeAnalyzerBoxes struct{ boxes []horseid.Box }

func (f fakeAnalyzerBoxes) DetectHorses(image.Image) ([]horseid.Box, error) { return f.boxes, nil }

// fakeAnalyzerPose — оценщик позы: 17 точек с выраженной головой.
func fakeAnalyzerPose() fakeAnalyzerPoser { return fakeAnalyzerPoser{} }

type fakeAnalyzerPoser struct{}

func (fakeAnalyzerPoser) EstimatePose(image.Image, [4]float64) ([]horseid.KeyPoint, error) {
	kps := make([]horseid.KeyPoint, 17)
	kps[0] = horseid.KeyPoint{X: 100, Y: 40, Score: 0.9}
	kps[1] = horseid.KeyPoint{X: 130, Y: 40, Score: 0.9}
	kps[2] = horseid.KeyPoint{X: 70, Y: 40, Score: 0.9}
	kps[3] = horseid.KeyPoint{X: 120, Y: 20, Score: 0.9}
	kps[4] = horseid.KeyPoint{X: 80, Y: 20, Score: 0.9}
	kps[11] = horseid.KeyPoint{X: 150, Y: 160, Score: 0.8}
	kps[12] = horseid.KeyPoint{X: 60, Y: 160, Score: 0.8}
	return kps, nil
}

// fakeAnalyzerEmbedder — эмбеддер с постоянными векторами.
type fakeAnalyzerEmbedder struct{}

func (fakeAnalyzerEmbedder) EmbedHead([]byte) ([]float64, error) {
	v := make([]float64, horseid.EmbedDim)
	for i := range v {
		v[i] = 0.5
	}
	return v, nil
}

func (fakeAnalyzerEmbedder) EmbedBody([]byte) ([]float64, error) {
	v := make([]float64, horseid.EmbedDim)
	for i := range v {
		v[i] = 0.25
	}
	return v, nil
}

// analyzerPNG — маленький валидный PNG.
func analyzerPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// TestHorseAnalyzerPrediction — адаптер: один Prediction с версией horseid
// и 512-мерным вектором concat(head256, body256).
func TestHorseAnalyzerPrediction(t *testing.T) {
	a := NewHorseAnalyzer(
		fakeAnalyzerBoxes{boxes: []horseid.Box{{Box: [4]float64{20, 20, 180, 180}, Confidence: 0.75}}},
		fakeAnalyzerPose(),
		fakeAnalyzerEmbedder{},
	)

	preds, err := a.Analyze(context.Background(), analyzerPNG(t), "image/png")
	require.NoError(t, err)
	require.Len(t, preds, 1)
	require.Equal(t, 0.75, preds[0].Confidence)
	require.Equal(t, horseid.Version, preds[0].Descriptor.Version)
	require.Len(t, preds[0].Descriptor.Vector, 2*horseid.EmbedDim)
}

// TestHorseAnalyzerNoDetection — лошади нет: inference.ErrNoAnimalDetected.
func TestHorseAnalyzerNoDetection(t *testing.T) {
	a := NewHorseAnalyzer(fakeAnalyzerBoxes{}, fakeAnalyzerPose(), fakeAnalyzerEmbedder{})
	_, err := a.Analyze(context.Background(), analyzerPNG(t), "image/png")
	require.ErrorIs(t, err, inference.ErrNoAnimalDetected)
}

// TestHorseAnalyzerSatisfiesAnalyzer — сигнатура соответствует Analyzer.
func TestHorseAnalyzerSatisfiesAnalyzer(t *testing.T) {
	var _ Analyzer = NewHorseAnalyzer(fakeAnalyzerBoxes{}, fakeAnalyzerPose(), fakeAnalyzerEmbedder{})
}

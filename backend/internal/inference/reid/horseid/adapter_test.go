package horseid

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

// fakeBoxes — детектор боксов: отдаёт заранее заданный список.
type fakeBoxes struct {
	boxes []Box
	err   error
}

func (f fakeBoxes) DetectHorses(image.Image) ([]Box, error) { return f.boxes, f.err }

// fakePose — оценщик позы: отдаёт заранее заданные точки.
type fakePose struct {
	kps []KeyPoint
	err error
}

func (f fakePose) EstimatePose(image.Image, [4]float64) ([]KeyPoint, error) { return f.kps, f.err }

// fakeEmbedder — эмбеддер: постоянные векторы и счётчики вызовов.
type fakeEmbedder struct {
	head, body           []float64
	headCalls, bodyCalls int
	err                  error
}

func (f *fakeEmbedder) EmbedHead([]byte) ([]float64, error) {
	f.headCalls++
	return f.head, f.err
}

func (f *fakeEmbedder) EmbedBody([]byte) ([]float64, error) {
	f.bodyCalls++
	return f.body, f.err
}

// samplePNG кодирует небольшое изображение в PNG.
func samplePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// filled возвращает вектор длины n со значением v.
func filled(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// keypointsWithHead — 17 точек с выраженной головой (нос, глаза, уши) и бёдрами.
func keypointsWithHead() []KeyPoint {
	kps := make([]KeyPoint, 17)
	kps[0] = KeyPoint{X: 100, Y: 40, Score: 0.9}   // нос
	kps[1] = KeyPoint{X: 130, Y: 40, Score: 0.9}   // глаз
	kps[2] = KeyPoint{X: 70, Y: 40, Score: 0.9}    // глаз
	kps[3] = KeyPoint{X: 120, Y: 20, Score: 0.9}   // ухо
	kps[4] = KeyPoint{X: 80, Y: 20, Score: 0.9}    // ухо
	kps[11] = KeyPoint{X: 150, Y: 160, Score: 0.8} // бедро
	kps[12] = KeyPoint{X: 60, Y: 160, Score: 0.8}  // бедро
	return kps
}

// TestPipelineWithHead — путь с найденной головой: 512-мерный вектор
// concat(head256, body256), head идёт первым, уверенность и Detection заполнены.
func TestPipelineWithHead(t *testing.T) {
	emb := &fakeEmbedder{head: filled(EmbedDim, 0.5), body: filled(EmbedDim, 0.25)}
	p := &horsePipeline{
		boxes: fakeBoxes{boxes: []Box{{Box: [4]float64{20, 20, 180, 180}, Confidence: 0.87}}},
		pose:  fakePose{kps: keypointsWithHead()},
		embed: emb,
	}

	res, err := p.Analyze(samplePNG(t, 200, 200))
	require.NoError(t, err)
	require.NotNil(t, res.Detection)
	require.True(t, res.Embedding.HasHead)
	require.True(t, res.Embedding.HasBody)
	require.Equal(t, 0.87, res.Confidence)
	require.Equal(t, 1, res.Detection.NumHorses)
	require.NotNil(t, res.Detection.HeadBox)
	require.Equal(t, 1, emb.headCalls)
	require.Equal(t, 1, emb.bodyCalls)

	vec := concatVector(res.Embedding)
	require.Len(t, vec, 2*EmbedDim)
	require.Equal(t, filled(EmbedDim, 0.5), vec[:EmbedDim])
	require.Equal(t, filled(EmbedDim, 0.25), vec[EmbedDim:])
}

// TestPipelineWithoutHead — голову найти не удалось: HasHead = false, вектор =
// body256, эмбеддер головы не вызывается.
func TestPipelineWithoutHead(t *testing.T) {
	kps := keypointsWithHead()
	kps[0] = KeyPoint{X: 100, Y: 40, Score: 0.1} // нос ниже порога
	emb := &fakeEmbedder{head: filled(EmbedDim, 0.5), body: filled(EmbedDim, 0.25)}
	p := &horsePipeline{
		boxes: fakeBoxes{boxes: []Box{{Box: [4]float64{20, 20, 180, 180}, Confidence: 0.6}}},
		pose:  fakePose{kps: kps},
		embed: emb,
	}

	res, err := p.Analyze(samplePNG(t, 200, 200))
	require.NoError(t, err)
	require.False(t, res.Embedding.HasHead)
	require.True(t, res.Embedding.HasBody)
	require.Nil(t, res.Detection.HeadBox)
	require.Zero(t, emb.headCalls)
	require.Equal(t, 1, emb.bodyCalls)

	vec := concatVector(res.Embedding)
	require.Len(t, vec, EmbedDim)
	require.Equal(t, filled(EmbedDim, 0.25), vec)
}

// TestPipelineNoDetection — пустой список боксов: inference.ErrNoAnimalDetected.
func TestPipelineNoDetection(t *testing.T) {
	p := &horsePipeline{
		boxes: fakeBoxes{boxes: nil},
		pose:  fakePose{kps: keypointsWithHead()},
		embed: &fakeEmbedder{},
	}

	_, err := p.Analyze(samplePNG(t, 200, 200))
	require.ErrorIs(t, err, inference.ErrNoAnimalDetected)

	_, err = p.Detect(samplePNG(t, 200, 200))
	require.ErrorIs(t, err, inference.ErrNoAnimalDetected)
}

// TestPipelineDetect — контрактный Detector: возвращает Detection без эмбеддингов.
func TestPipelineDetect(t *testing.T) {
	p := &horsePipeline{
		boxes: fakeBoxes{boxes: []Box{{Box: [4]float64{20, 20, 180, 180}, Confidence: 0.9}}},
		pose:  fakePose{kps: keypointsWithHead()},
		embed: &fakeEmbedder{},
	}

	det, err := p.Detect(samplePNG(t, 200, 200))
	require.NoError(t, err)
	require.Equal(t, [4]float64{20, 20, 180, 180}, det.BodyBox)
	require.NotNil(t, det.HeadBox)
	require.Equal(t, 1, det.NumHorses)
	require.Equal(t, ViewFront, det.View) // нос (100) почти в центре бёдер (105)
}

// TestPipelineInvalidImage — нераспознанные байты: inference.ErrInvalidImage.
func TestPipelineInvalidImage(t *testing.T) {
	p := &horsePipeline{boxes: fakeBoxes{}, pose: fakePose{}, embed: &fakeEmbedder{}}
	_, err := p.Analyze([]byte("not an image"))
	require.ErrorIs(t, err, inference.ErrInvalidImage)
}

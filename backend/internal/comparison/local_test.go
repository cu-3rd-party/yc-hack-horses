package comparison

import (
	"bytes"
	"context"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/yolo"
	"github.com/stretchr/testify/require"
)

type detectorFunc func(context.Context, inference.ImageInput, *image.Rectangle) ([]yolo.InstanceEmbedding, error)

func (detect detectorFunc) EmbedInstances(ctx context.Context, input inference.ImageInput, box *image.Rectangle) ([]yolo.InstanceEmbedding, error) {
	return detect(ctx, input, box)
}

func indexedMask(t *testing.T, rows ...string) []byte {
	t.Helper()
	mask := image.NewPaletted(image.Rect(0, 0, len(rows[0]), len(rows)), color.Palette{color.Transparent, color.White})
	for y, row := range rows {
		for x, cell := range row {
			if cell == 'x' {
				mask.SetColorIndex(x, y, 1)
			}
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, mask))
	return buf.Bytes()
}

func TestLocalComparisonOrientsImageAndKeepsEachInstance(t *testing.T) {
	var photo bytes.Buffer
	require.NoError(t, jpeg.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 20, 40)), nil))
	// EXIF APP1, little-endian TIFF, orientation tag 6 (90 degrees clockwise).
	exif, err := hex.DecodeString("ffe1002245786966000049492a0008000000010012010300010000000600000000000000")
	require.NoError(t, err)
	data := bytes.Join([][]byte{photo.Bytes()[:2], exif, photo.Bytes()[2:]}, nil)
	mask := indexedMask(t, "xx", "x.")
	head := indexedMask(t, "x.", "..")
	calls := 0
	m := LocalModel{Detector: detectorFunc(func(_ context.Context, input inference.ImageInput, box *image.Rectangle) ([]yolo.InstanceEmbedding, error) {
		calls++
		require.Nil(t, box)
		require.Equal(t, image.Rect(0, 0, 40, 20), input.Decoded.Bounds())
		return []yolo.InstanceEmbedding{
			{Box: image.Rect(10, 0, 30, 20), MaskPNG: mask, Confidence: .98,
				Embedding: inference.EmbeddingResult{ModelVersion: "visual", Vector: []float32{1, 0}},
				Traits:    &reid.TraitEvidence{Values: map[string]float64{"coat_warmth": 1.5, "coat_light": .25, "head_ratio": 2}, Layers: map[string][]byte{"coat_light": head}}},
			{Box: image.Rect(30, 0, 40, 10), MaskPNG: head, Confidence: .78,
				Embedding: inference.EmbeddingResult{ModelVersion: "visual", Vector: []float32{0, 1}}},
		}, nil
	})}
	result, err := m.Analyze(t.Context(), data, "image/jpeg")
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Len(t, result, 2)
	require.Equal(t, []Point{{.25, 0}, {.75, 0}, {.75, .5}, {.5, .5}, {.5, 1}, {.25, 1}}, result[0].Annotation.HorsePolygon)
	require.Equal(t, &PartMask{PNG: mask, Box: [4]float64{.25, 0, .75, 1}}, result[0].Annotation.PartMask)
	require.Equal(t, &PartMask{PNG: head, Box: [4]float64{.75, 0, 1, .5}}, result[1].Annotation.PartMask)
	coat := []Point{{.25, 0}, {.5, 0}, {.5, .5}, {.25, .5}}
	require.Equal(t, []Trait{{"coat_light", "0.25", coat}, {"coat_warmth", "1.5", coat}, {"head_ratio", "2", nil}}, result[0].Annotation.Traits)
	require.Equal(t, []Point{{.75, 0}, {.875, 0}, {.875, .25}, {.75, .25}}, result[1].Annotation.HorsePolygon)
	require.Equal(t, []Trait{}, result[1].Annotation.Traits)
	require.Equal(t, .98, result[0].Confidence)
	require.Equal(t, .78, result[1].Confidence)
	require.Equal(t, Descriptor{Version: "visual", Vector: []float64{1, 0}}, result[0].Descriptor)
	require.Equal(t, Descriptor{Version: "visual", Vector: []float64{0, 1}}, result[1].Descriptor)
}

func TestMaskPolygonUsesLargestExterior(t *testing.T) {
	mask := indexedMask(t, "xxx.", "x.x.", "xxx.", "...x")
	bounds := image.Rect(0, 0, 4, 4)
	points, err := maskPolygon(mask, bounds, bounds)
	require.NoError(t, err)
	require.Equal(t, []Point{{0, 0}, {.75, 0}, {.75, .75}, {0, .75}}, points)
}

func TestLocalBoxRetrievalWithoutMask(t *testing.T) {
	row := yolo.InstanceEmbedding{
		Box: image.Rect(12, 24, 18, 32), Confidence: .8, MeasurementError: "part heads unavailable",
		Embedding: inference.EmbeddingResult{ModelVersion: reid.BoxVersion, Vector: []float32{1, 0}},
	}
	p, err := localPrediction(row, image.Rect(10, 20, 20, 40))
	require.NoError(t, err)
	require.Equal(t, []Point{{.2, .2}, {.8, .2}, {.8, .6}, {.2, .6}}, p.Annotation.HorsePolygon)
	require.Nil(t, p.Annotation.PartMask)
	require.Empty(t, p.Annotation.Traits)
	require.Equal(t, reid.BoxVersion, p.Version)
}

func TestLocalComparisonFailures(t *testing.T) {
	photo := indexedMask(t, "xx", "xx")
	box := image.Rect(0, 0, 1, 1)
	tests := []struct {
		name      string
		rows      []yolo.InstanceEmbedding
		err, want error
	}{
		{name: "no horses", err: inference.ErrNoAnimalDetected},
		{name: "weights unavailable", err: inference.ErrModelUnavailable, want: inference.ErrModelUnavailable},
		{name: "detector only", rows: []yolo.InstanceEmbedding{{Box: box}}, want: ErrLocalSegmentation},
		{name: "measurement failed", rows: []yolo.InstanceEmbedding{{Box: box, MeasurementError: "model file missing"}}, want: ErrLocalSegmentation},
		{name: "invalid mask", rows: []yolo.InstanceEmbedding{{Box: box, MaskPNG: []byte("invalid")}}, want: ErrModel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := LocalModel{Detector: detectorFunc(func(context.Context, inference.ImageInput, *image.Rectangle) ([]yolo.InstanceEmbedding, error) {
				return tt.rows, tt.err
			})}
			result, err := model.Analyze(t.Context(), photo, "image/png")
			require.ErrorIs(t, err, tt.want)
			if tt.want == nil {
				require.Equal(t, []Prediction{}, result)
			}
		})
	}
	_, err := (LocalModel{}).Analyze(t.Context(), []byte("invalid"), "image/jpeg")
	require.ErrorIs(t, err, inference.ErrInvalidImage)
}

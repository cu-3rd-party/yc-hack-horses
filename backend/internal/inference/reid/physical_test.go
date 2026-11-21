package reid

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/stretchr/testify/require"
)

func TestVisualSimilarityAcrossDescriptorVersions(t *testing.T) {
	va, vb := make([]float32, Dimension), make([]float32, Dimension)
	va[0], vb[0], vb[1] = 2, 3, 4
	a, err := visualEmbedding(va)
	require.NoError(t, err)
	b, err := visualEmbedding(vb)
	require.NoError(t, err)
	asLegacy := func(e inference.EmbeddingResult) inference.EmbeddingResult {
		old := make([]float32, Dimension+10)
		for i, x := range e.Vector {
			old[i] = float32(math.Sqrt(.9) * float64(x))
		}
		return inference.EmbeddingResult{ModelVersion: legacyVersion, Vector: old}
	}
	la, lb := asLegacy(a), asLegacy(b)
	for _, tail := range []float32{0, .14142136, -.14142136} {
		lb.Vector[Dimension] = tail
		for _, left := range []inference.EmbeddingResult{a, la} {
			for _, right := range []inference.EmbeddingResult{b, lb} {
				require.NoError(t, Validate(right))
				score, err := Similarity(left, right)
				require.NoError(t, err)
				require.InDelta(t, .6, score, 1e-6)
			}
		}
	}
	_, err = visualEmbedding(make([]float32, Dimension))
	require.Error(t, err)
	b.ModelVersion = "reference-v1"
	_, err = Similarity(a, b)
	require.Error(t, err)
}

func TestPhysicalUsesSegmentPixelsAndVisibility(t *testing.T) {
	var samples []pixel
	for i := range 100 {
		x, y := float64(i%10), float64(i/10)
		samples = append(samples, pixel{x: 50 + x, y: 50 + y, body: true, luma: .3, warmth: .2},
			pixel{x: 15 + x, y: 20 + y, body: true, head: true, luma: .4},
			pixel{x: 30 + x, y: 30 + y, body: true, head: true, muzzle: true, luma: .70},
			pixel{x: 30 + x, y: 30 + y, body: true, head: true, muzzle: true, luma: .12},
			pixel{x: 50 + x, y: 50 + y, luma: .9}) // bright background
	}
	got := measurements(samples, 100, 100)
	for i, v := range []float64{.3, .2, .4, .3} {
		require.True(t, got[i].visible)
		require.InDelta(t, v, got[i].value, 1e-6)
	}
	require.False(t, got[4].visible)
	require.Equal(t, [traitCount]measure{}, measurements(samples[:5], 100, 100))
	// A lateral, elongated head produces a valid ratio; same evidence viewed
	// with ears and muzzle vertically aligned must suppress the shape coordinate.
	var side []pixel
	for y := range 8 {
		for x := range 32 {
			side = append(side, pixel{x: float64(x), y: float64(y), body: true, head: true, ears: x < 4, muzzle: x >= 26, luma: .5})
		}
	}
	require.True(t, measurements(side, 32, 8)[4].visible)
	for i := range side {
		side[i].x, side[i].y = side[i].y, side[i].x
	}
	require.False(t, measurements(side, 8, 32)[4].visible)
}

func TestRegionAlignmentAndResampling(t *testing.T) {
	img := image.NewRGBA(image.Rect(30, 40, 44, 50))
	box := image.Rect(32, 42, 40, 48)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			img.SetRGBA(x, y, color.RGBA{200, 50, 10, 255})
		}
	}
	for _, cubic := range []bool{false, true} {
		for _, size := range []int{2, 16} {
			data := tensorPixels(img, box, size, cubic)
			for i := range size * size {
				require.InDelta(t, (200./255-.485)/.229, data[i], 1e-5)
			}
		}
	}
	m := partMaps{box: box}
	for i := range m.channels[0] {
		m.channels[0][i] = 1
	}
	require.True(t, m.contains(0, 32, 42))
	require.False(t, m.contains(0, 40, 42))
	mask := []bool{true, false, true, true, false, true}
	require.Equal(t, []bool{false, false, true, false, false, true}, largestComponent([]bool{false, false, true, false, false, true}, 3))
	require.Equal(t, []bool{true, false, false, true, false, false}, largestComponent(mask, 3))
	var weights [headWidth * 4]float32
	weights[headWidth-1] = float32(math.Log(3))
	tokens := make([]float32, 257*tokenDim)
	m = decodeParts(tokens, weights[:], box)
	require.InDelta(t, .75, m.channels[0][0], 1e-6)
}

package reid

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExplainMaskShowsOnlyMeasuredCells(t *testing.T) {
	box := image.Rect(17, 9, 65, 33)
	img := image.NewRGBA(image.Rect(0, 0, 80, 40))
	w, h := box.Dx(), box.Dy()
	samples := make([]pixel, w*h)
	for y := range h {
		for x := range w {
			c := color.RGBA{R: 90, G: 80, B: 70, A: 255}
			if x == 18 || x == 40 {
				c = color.RGBA{A: 255} // shadow on coat and head
			}
			img.SetRGBA(box.Min.X+x, box.Min.Y+y, c)
			p, _ := sampleAt(img, box, x, y, w, h)
			p.body, p.head, p.ears, p.muzzle = true, x >= 36, x == 36, x >= 44
			samples[y*w+x] = p
		}
	}
	raw, err := maskPNG(samples, box)
	require.NoError(t, err)
	e, err := ExplainMask(img, box, raw)
	require.NoError(t, err)
	expect := measurements(samples, float64(w), float64(h))
	for i, tval := range expect {
		v, ok := e.Values[traitKeys[i]]
		require.Equal(t, tval.visible, ok)
		require.Equal(t, tval.value, v)
	}
	layer := func(name string) *image.Paletted {
		t.Helper()
		img, err := png.Decode(bytes.NewReader(e.Layers[name]))
		require.NoError(t, err)
		return img.(*image.Paletted)
	}
	coat := layer("coat_light")
	require.Zero(t, coat.ColorIndexAt(3, 8)) // outside mid-body window
	require.Zero(t, coat.ColorIndexAt(18, 8))
	require.NotZero(t, coat.ColorIndexAt(19, 8))
	contrast := layer("muzzle_contrast")
	require.Zero(t, contrast.ColorIndexAt(36, 8)) // ear
	require.Zero(t, contrast.ColorIndexAt(40, 8)) // clipped cheek
	require.EqualValues(t, 2, contrast.ColorIndexAt(41, 8))
	require.EqualValues(t, 3, contrast.ColorIndexAt(45, 8))
	require.NotContains(t, e.Values, "head_ratio") // pose gate fails
	require.NotContains(t, e.Layers, "head_ratio")
}

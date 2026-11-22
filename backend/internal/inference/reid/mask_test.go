package reid

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaskPNGEncodesOverlappingPartsInCropCoordinates(t *testing.T) {
	box := image.Rect(21, 43, 27, 44)
	samples := []pixel{{}, {body: true}, {body: true, head: true}, {body: true, head: true, ears: true}, {body: true, head: true, muzzle: true}, {body: true, head: true, ears: true, muzzle: true}}
	raw, err := maskPNG(samples, box)
	require.NoError(t, err)
	img, err := png.Decode(bytes.NewReader(raw))
	require.NoError(t, err)
	mask := img.(*image.Paletted)
	require.Equal(t, image.Rect(0, 0, 6, 1), mask.Bounds())
	for x, want := range []uint8{0, 1, 3, 7, 11, 15} {
		require.Equal(t, want, mask.ColorIndexAt(x, 0))
		_, _, _, a := mask.At(x, 0).RGBA()
		if x == 0 {
			require.Zero(t, a)
		} else {
			require.EqualValues(t, 65535, a)
		}
	}
	raw, err = maskPNG(make([]pixel, len(samples)), box)
	require.NoError(t, err)
	require.Empty(t, raw)
}

func TestMaskBackgroundKeepsOnlyBodyBeforeResizing(t *testing.T) {
	box := image.Rect(11, 7, 268, 13)
	makeCrop := func(bg uint8) *image.RGBA {
		img := image.NewRGBA(box.Inset(-2))
		for y := box.Min.Y; y < box.Max.Y; y++ {
			for x := box.Min.X; x < box.Max.X; x++ {
				c := color.RGBA{bg, bg, bg, 255}
				if x < box.Min.X+128 {
					c = color.RGBA{201, 159, 82, 255}
				}
				img.SetRGBA(x, y, c)
			}
		}
		return img
	}
	samples := make([]pixel, 128*box.Dy())
	for i := range samples {
		samples[i].body = i%128 < 64
		samples[i].head = true // Only the body channel can keep RGB pixels.
	}
	a, b := makeCrop(1), makeCrop(255)
	require.True(t, maskBackground(a, box, samples))
	require.True(t, maskBackground(b, box, samples))
	require.Equal(t, color.RGBA{201, 159, 82, 255}, a.RGBAAt(box.Min.X+127, box.Min.Y))
	require.Equal(t, color.RGBA{127, 127, 127, 255}, a.RGBAAt(box.Min.X+128, box.Min.Y))
	require.Equal(t, tensorPixels(a, box, 384, false), tensorPixels(b, box, 384, false))
	require.False(t, maskBackground(a, box, make([]pixel, len(samples))))
}

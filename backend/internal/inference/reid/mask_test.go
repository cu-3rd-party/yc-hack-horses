package reid

import (
	"bytes"
	"image"
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

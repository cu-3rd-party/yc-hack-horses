package reid

import (
	"image"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCentralRectTakesEightyPercentCenter(t *testing.T) {
	box := image.Rect(0, 0, 100, 60)
	got := centralRect(box)
	require.Equal(t, 10, got.Min.X) // 100 * 0.1
	require.Equal(t, 6, got.Min.Y)  // 60 * 0.1
	require.Equal(t, 90, got.Max.X)
	require.Equal(t, 54, got.Max.Y)
	require.Equal(t, 80, got.Dx())
	require.Equal(t, 48, got.Dy())
}

func TestCentralRectOnTinyBoxKeepsOriginal(t *testing.T) {
	tiny := image.Rect(0, 0, 1, 1)
	require.Equal(t, tiny, centralRect(tiny))
}

func TestIsSilhouetteSeparatesForegroundVariants(t *testing.T) {
	require.True(t, IsSilhouette(Version))
	require.True(t, IsSilhouette(publishedHeadsVersion))
	require.True(t, IsSilhouette(centralVersion))
	require.False(t, IsSilhouette(BoxVersion))
}

package inference

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

// syntheticImage — детерминированное синтетическое изображение: верх тёмный
// (грива), тело цвета дюн, низ светлый (отметины).
func syntheticImage(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		var c color.RGBA
		switch {
		case y < h/4:
			c = color.RGBA{R: 40, G: 40, B: 40, A: 255} // тёмная грива
		case y > 3*h/4:
			c = color.RGBA{R: 240, G: 240, B: 240, A: 255} // светлые отметины
		default:
			c = color.RGBA{R: 200, G: 160, B: 110, A: 255} // тело
		}
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestReferenceEngineExtractTraits(t *testing.T) {
	eng := NewReference(nil)
	res, err := eng.ExtractTraits(context.Background(), ImageInput{Reader: bytes.NewReader(syntheticImage(100, 200))})
	require.NoError(t, err)
	require.Equal(t, referenceMetadata.Version, res.ModelVersion, "model_version")
	require.Positive(t, res.Confidence, "confidence must be in (0,1)")
	require.Less(t, res.Confidence, 1.0, "confidence must be in (0,1)")
	require.Equal(t, "dark", res.Traits["mane"], "mane")
	require.Equal(t, "blaze", res.Traits["markings"], "markings")
	require.NotEqual(t, "", res.Traits["coat"], "coat must be classified")
	for _, k := range CanonicalKeys {
		require.Contains(t, res.Traits, k, "trait %q missing from result", k)
	}
}

func TestReferenceEngineEmbed(t *testing.T) {
	eng := NewReference(nil)
	res, err := eng.Embed(context.Background(), ImageInput{Reader: bytes.NewReader(syntheticImage(64, 64))})
	require.NoError(t, err)
	require.Equal(t, referenceMetadata.Version, res.ModelVersion, "model_version")
	require.Len(t, res.Vector, 64, "embedding length")
	// детерминизм: то же изображение — тот же вектор
	res2, err := eng.Embed(context.Background(), ImageInput{Reader: bytes.NewReader(syntheticImage(64, 64))})
	require.NoError(t, err)
	require.Equal(t, res2.Vector, res.Vector, "embedding must be deterministic")
}

func TestReferenceEngineInvalidImage(t *testing.T) {
	eng := NewReference(nil)
	_, err := eng.ExtractTraits(context.Background(), ImageInput{Reader: bytes.NewReader([]byte("not an image"))})
	require.ErrorIs(t, err, ErrInvalidImage, "want ErrInvalidImage for non-image data")
	_, err = eng.ExtractTraits(context.Background(), ImageInput{})
	require.ErrorIs(t, err, ErrInvalidImage, "want ErrInvalidImage for empty input")
}

func TestUnavailableEngine(t *testing.T) {
	eng := NewUnavailable()
	_, err := eng.ExtractTraits(context.Background(), ImageInput{})
	require.ErrorIs(t, err, ErrModelUnavailable, "want ErrModelUnavailable")
	_, err = eng.Embed(context.Background(), ImageInput{})
	require.ErrorIs(t, err, ErrModelUnavailable, "want ErrModelUnavailable")
}

type fakeBlobs struct{ data map[string][]byte }

func (f fakeBlobs) Get(_ context.Context, sha string) ([]byte, error) {
	if d, ok := f.data[sha]; ok {
		return d, nil
	}
	return nil, ErrInvalidImage
}

func TestReferenceEngineViaBlobStore(t *testing.T) {
	img := syntheticImage(80, 80)
	eng := NewReference(fakeBlobs{data: map[string][]byte{"abc123": img}})
	res, err := eng.ExtractTraits(context.Background(), ImageInput{SHA256: "abc123"})
	require.NoError(t, err)
	require.NotEqual(t, "", res.Traits["coat"], "coat must be classified")
}

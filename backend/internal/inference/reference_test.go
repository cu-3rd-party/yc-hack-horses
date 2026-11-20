package inference

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
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
	if err != nil {
		t.Fatal(err)
	}
	if res.ModelVersion != referenceMetadata.Version {
		t.Fatalf("model_version = %q", res.ModelVersion)
	}
	if res.Confidence <= 0 || res.Confidence >= 1 {
		t.Fatalf("confidence must be in (0,1), got %v", res.Confidence)
	}
	if res.Traits["mane"] != "dark" {
		t.Fatalf("mane = %q, want dark", res.Traits["mane"])
	}
	if res.Traits["markings"] != "blaze" {
		t.Fatalf("markings = %q, want blaze", res.Traits["markings"])
	}
	if res.Traits["coat"] == "" {
		t.Fatal("coat must be classified")
	}
	for _, k := range CanonicalKeys {
		if _, ok := res.Traits[k]; !ok {
			t.Fatalf("trait %q missing from result", k)
		}
	}
}

func TestReferenceEngineEmbed(t *testing.T) {
	eng := NewReference(nil)
	res, err := eng.Embed(context.Background(), ImageInput{Reader: bytes.NewReader(syntheticImage(64, 64))})
	if err != nil {
		t.Fatal(err)
	}
	if res.ModelVersion != referenceMetadata.Version {
		t.Fatalf("model_version = %q", res.ModelVersion)
	}
	if len(res.Vector) != 64 {
		t.Fatalf("embedding length = %d, want 64", len(res.Vector))
	}
	// детерминизм: то же изображение — тот же вектор
	res2, err := eng.Embed(context.Background(), ImageInput{Reader: bytes.NewReader(syntheticImage(64, 64))})
	if err != nil {
		t.Fatal(err)
	}
	for i := range res.Vector {
		if res.Vector[i] != res2.Vector[i] {
			t.Fatalf("embedding not deterministic at %d", i)
		}
	}
}

func TestReferenceEngineInvalidImage(t *testing.T) {
	eng := NewReference(nil)
	if _, err := eng.ExtractTraits(context.Background(), ImageInput{Reader: bytes.NewReader([]byte("not an image"))}); err == nil {
		t.Fatal("want ErrInvalidImage for non-image data")
	} else if err.Error() == "" {
		t.Fatal("error must have message")
	}
	if _, err := eng.ExtractTraits(context.Background(), ImageInput{}); err == nil {
		t.Fatal("want ErrInvalidImage for empty input")
	}
}

func TestUnavailableEngine(t *testing.T) {
	eng := NewUnavailable()
	if _, err := eng.ExtractTraits(context.Background(), ImageInput{}); err != ErrModelUnavailable {
		t.Fatalf("want ErrModelUnavailable, got %v", err)
	}
	if _, err := eng.Embed(context.Background(), ImageInput{}); err != ErrModelUnavailable {
		t.Fatalf("want ErrModelUnavailable, got %v", err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if res.Traits["coat"] == "" {
		t.Fatal("coat must be classified")
	}
}

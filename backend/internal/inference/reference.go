package inference

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
)

// ReferenceEngine — детерминированный baseline-движок на цветовых гистограммах
// по областям изображения (stdlib image/*). README/ADR-0012: это НЕ обученная
// модель и НЕ идентификация животных — только baseline для отработки контракта.
//
// Точка расширения ONNX (V1): заменить ExtractTraits/Embed реализацией на
// onnxruntime-go — предобработка из ModelMetadata.PreprocessSpec, вход по
// ImageInput.SHA256 из BlobStore. Контракт движка при этом не меняется.
type ReferenceEngine struct {
	blobs BlobFetcher
}

// BlobFetcher — минимальная зависимость на BlobStore (уменьшает связность с
// storage-пакетом до вызова). Реализована storage.BlobStore.
type BlobFetcher interface {
	Get(ctx context.Context, sha256hex string) ([]byte, error)
}

// NewReference создаёт baseline-движок. blobs может быть nil, если вход всегда
// подаётся через ImageInput.Reader (тесты).
func NewReference(blobs BlobFetcher) ReferenceEngine {
	return ReferenceEngine{blobs: blobs}
}

// ExtractTraits вычисляет признаки по цветовым гистограммам горизонтальных
// областей изображения. Детерминированно: одно изображение — один результат.
func (e ReferenceEngine) ExtractTraits(ctx context.Context, input ImageInput) (TraitResult, error) {
	img, err := e.decode(ctx, input)
	if err != nil {
		return TraitResult{}, err
	}
	bands := bandMeans(img)
	traits := map[string]string{
		"coat":        classifyCoat(bands[bandBody]),
		"muzzle":      classifyMuzzle(bands[bandMuzzle]),
		"markings":    classifyMarkings(bands[bandLower]),
		"mane":        classifyMane(bands[bandMane]),
		"ears":        "erect",
		"head":        "straight",
		"proportions": classifyProportions(img.Bounds()),
		"scars":       "none",
	}
	return TraitResult{
		ModelVersion: referenceMetadata.Version,
		Traits:       traits,
		Confidence:   0.3, // baseline: низкая уверенность, подтверждение человеком обязательно
	}, nil
}

// Embed — 64-мерный детерминированный вектор: средние RGB по горизонтальным
// полосам + нормированные суммы по каналам. Базовый вариант для поиска похожих.
func (e ReferenceEngine) Embed(ctx context.Context, input ImageInput) (EmbeddingResult, error) {
	img, err := e.decode(ctx, input)
	if err != nil {
		return EmbeddingResult{}, err
	}
	bands := bandMeans(img)
	vector := make([]float32, 0, 64)
	for _, b := range bands {
		vector = append(vector, float32(b.r), float32(b.g), float32(b.b))
	}
	// дополнение до фиксированной длины: суммы по каналам + нормированные значения
	var sums [3]float64
	for _, b := range bands {
		sums[0] += b.r
		sums[1] += b.g
		sums[2] += b.b
	}
	for c := 0; c < 3; c++ {
		vector = append(vector, float32(sums[c]/float64(len(bands))))
	}
	for len(vector) < 64 {
		i := len(vector)
		vector = append(vector, float32(sums[i%3]/float64(len(bands)*(1+i))))
	}
	return EmbeddingResult{ModelVersion: referenceMetadata.Version, Vector: vector}, nil
}

// --- внутренняя логика гистограмм ---

const (
	numBands   = 16 // горизонтальных полос изображения
	bandHead   = 0  // верхняя полоса — голова
	bandMane   = 1  // вторая полоса — грива
	bandMuzzle = 8  // середина — морда
	bandBody   = 11 // тело
	bandLower  = 15 // нижняя полоса — отметины/шрамы
)

type bandMean struct{ r, g, b float64 }

// decode получает байты (BlobStore или Reader) и декодирует изображение.
func (e ReferenceEngine) decode(ctx context.Context, input ImageInput) (image.Image, error) {
	var data []byte
	switch {
	case input.Reader != nil:
		b, err := io.ReadAll(input.Reader)
		if err != nil {
			return nil, fmt.Errorf("%w: read: %v", ErrInvalidImage, err)
		}
		data = b
	case input.SHA256 != "" && e.blobs != nil:
		b, err := e.blobs.Get(ctx, input.SHA256)
		if err != nil {
			return nil, fmt.Errorf("%w: blob %s: %v", ErrInvalidImage, input.SHA256, err)
		}
		data = b
	default:
		return nil, fmt.Errorf("%w: no reader or blob store", ErrInvalidImage)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrInvalidImage, err)
	}
	return img, nil
}

// bandMeans — средние RGB по numBands горизонтальным полосам.
func bandMeans(img image.Image) []bandMean {
	bounds := img.Bounds()
	h := bounds.Dy()
	out := make([]bandMean, numBands)
	counts := make([]int, numBands)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		band := (y - bounds.Min.Y) * numBands / h
		if band >= numBands {
			band = numBands - 1
		}
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			m := &out[band]
			m.r += float64(r >> 8)
			m.g += float64(g >> 8)
			m.b += float64(b >> 8)
			counts[band]++
		}
	}
	for i := range out {
		if counts[i] == 0 {
			continue
		}
		n := float64(counts[i])
		out[i].r /= n
		out[i].g /= n
		out[i].b /= n
	}
	return out
}

// classifyCoat — окрас по средней яркости и соотношению каналов тела.
func classifyCoat(b bandMean) string {
	sum := b.r + b.g + b.b
	if sum < 150 {
		return "black"
	}
	if sum > 690 {
		return "gray"
	}
	if b.r > b.g && b.g > b.b {
		return "bay"
	}
	if b.g >= b.r && b.g >= b.b {
		return "dun"
	}
	if b.r > b.b {
		return "chestnut"
	}
	return "black"
}

// classifyMuzzle — морда по яркости средней полосы.
func classifyMuzzle(b bandMean) string {
	sum := b.r + b.g + b.b
	switch {
	case sum > 700:
		return "white"
	case sum > 420:
		return "light"
	case sum > 180:
		return "gray"
	default:
		return "dark"
	}
}

// classifyMarkings — отметины по наличию светлых пикселей в нижней полосе.
func classifyMarkings(b bandMean) string {
	avg := (b.r + b.g + b.b) / 3
	switch {
	case avg > 220:
		return "blaze"
	case avg > 160:
		return "sock"
	case avg > 60:
		return "none"
	default:
		return "none"
	}
}

// classifyMane — грива по яркости второй полосы.
func classifyMane(b bandMean) string {
	avg := (b.r + b.g + b.b) / 3
	if avg > 140 {
		return "light"
	}
	return "dark"
}

// classifyProportions — пропорции по соотношению сторон изображения.
func classifyProportions(bounds image.Rectangle) string {
	w := bounds.Dx()
	h := bounds.Dy()
	if h == 0 {
		return "compact"
	}
	if w*4 > h*3 {
		return "compact"
	}
	return "elongated"
}

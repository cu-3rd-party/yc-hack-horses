package yolo

import (
	"context"
	"image"
)

// Crops — кропы вокруг детекции: весь корпус и голова/морда.
type Crops struct {
	Body image.Image
	Head image.Image
	Box  image.Rectangle // бокс детекции в координатах исходного фото
}

// TraitDelegate — стратегия предзаполнения трейтов по кропам. Заменяется
// будущим трейт-движком (обученным на лошадях Пржевальского) без изменения
// детекции: человек подтверждает/правит результат в trait_snapshots.
type TraitDelegate interface {
	ExtractTraitsFromCrops(ctx context.Context, crops Crops) (traits map[string]string, confidence float64, err error)
}

// HistogramDelegate — гистограммная эвристика по кропам (аналог
// ReferenceEngine, но по областям: голова/корпус отдельно). Это НЕ обученная
// модель: только предзаполнение для человека, НЕ идентификация.
type HistogramDelegate struct{}

// NewHistogramDelegate — дефолтная стратегия.
func NewHistogramDelegate() HistogramDelegate { return HistogramDelegate{} }

// ExtractTraitsFromCrops классифицирует признаки по средним цветам кропов.
func (HistogramDelegate) ExtractTraitsFromCrops(ctx context.Context, crops Crops) (map[string]string, float64, error) {
	body := regionMean(crops.Body)
	head := regionMean(crops.Head)
	traits := map[string]string{
		"coat":     classifyCoatMean(body),
		"muzzle":   classifyMuzzleMean(head),
		"mane":     classifyManeMean(head),
		"head":     "straight",
		"ears":     "erect",
		"scars":    "none",
		"markings": classifyMarkingsMean(body),
	}
	// Пропорции: отношение сторон бокса детекции.
	traits["proportions"] = classifyProportionsBox(crops.Box)
	return traits, 0.3, nil
}

// regionMean — средний RGB области.
func regionMean(img image.Image) (m bandMean) {
	if img == nil {
		return
	}
	b := img.Bounds()
	if b.Empty() {
		return
	}
	var n float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			m.r += float64(r >> 8)
			m.g += float64(g >> 8)
			m.b += float64(bl >> 8)
			n++
		}
	}
	if n > 0 {
		m.r /= n
		m.g /= n
		m.b /= n
	}
	return
}

// bandMean — средние RGB (порядок полей совпадает с reference-эвристикой).
type bandMean struct{ r, g, b float64 }

// classifyCoatMean — окрас корпуса по средней яркости и каналам.
func classifyCoatMean(m bandMean) string {
	sum := m.r + m.g + m.b
	switch {
	case sum < 150:
		return "black"
	case sum > 690:
		return "gray"
	case m.r > m.g && m.g > m.b:
		return "bay"
	case m.g >= m.r && m.g >= m.b:
		return "dun"
	case m.r > m.b:
		return "chestnut"
	default:
		return "black"
	}
}

// classifyMuzzleMean — морда по яркости кропа головы.
func classifyMuzzleMean(m bandMean) string {
	sum := m.r + m.g + m.b
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

// classifyManeMean — грива по яркости кропа головы.
func classifyManeMean(m bandMean) string {
	if (m.r+m.g+m.b)/3 > 140 {
		return "light"
	}
	return "dark"
}

// classifyMarkingsMean — отметины по наличию светлых пикселей корпуса.
func classifyMarkingsMean(m bandMean) string {
	avg := (m.r + m.g + m.b) / 3
	switch {
	case avg > 220:
		return "blaze"
	case avg > 160:
		return "sock"
	default:
		return "none"
	}
}

// classifyProportionsBox — пропорции по соотношению сторон бокса.
func classifyProportionsBox(b image.Rectangle) string {
	w, h := b.Dx(), b.Dy()
	if h == 0 {
		return "compact"
	}
	if w*4 > h*3 {
		return "compact"
	}
	return "elongated"
}

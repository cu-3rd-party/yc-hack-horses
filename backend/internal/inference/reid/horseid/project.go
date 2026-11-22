package horseid

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// projectionBytes — размер файла головы: W[EmbedDim][DinoDim] + b[EmbedDim],
// всё little-endian float32, без заголовка.
const projectionBytes = 4 * (EmbedDim*DinoDim + EmbedDim)

// l2Normalize нормирует вектор на месте; нулевая или нечисловая норма — ошибка.
func l2Normalize[T float32 | float64](v []T) error {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	norm := math.Sqrt(sum)
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return fmt.Errorf("horseid: нулевая норма вектора")
	}
	for i := range v {
		v[i] = T(float64(v[i]) / norm)
	}
	return nil
}

// Projection — обученная ArcFace-голова одной части (head или body):
// линейный слой DinoDim → EmbedDim из export_heads.py.
type Projection struct {
	w []float32 // EmbedDim строк по DinoDim
	b []float32 // EmbedDim
}

// LoadProjection читает файл весов heads_dinov2_<part>.f32:
// сначала W[256][768] построчно, затем b[256], little-endian float32.
func LoadProjection(path string) (*Projection, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("horseid: чтение головы %s: %w", path, err)
	}
	if len(raw) != projectionBytes {
		return nil, fmt.Errorf("horseid: %s: ожидалось %d байт, получено %d", path, projectionBytes, len(raw))
	}
	p := &Projection{
		w: make([]float32, EmbedDim*DinoDim),
		b: make([]float32, EmbedDim),
	}
	for i := range p.w {
		v := math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, fmt.Errorf("horseid: %s: нечисловой вес W[%d]", path, i)
		}
		p.w[i] = v
	}
	offset := len(p.w) * 4
	for i := range p.b {
		v := math.Float32frombits(binary.LittleEndian.Uint32(raw[offset+i*4:]))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, fmt.Errorf("horseid: %s: нечисловое смещение b[%d]", path, i)
		}
		p.b[i] = v
	}
	return p, nil
}

// Apply считает e = normalize(W·x + b) для нормированного вектора DINOv2 (768).
// Возвращает 256 значений с единичной L2-нормой.
func (p *Projection) Apply(x []float32) ([]float64, error) {
	if len(x) != DinoDim {
		return nil, fmt.Errorf("horseid: проекция ждёт %d значений, получено %d", DinoDim, len(x))
	}
	if p == nil || len(p.w) != EmbedDim*DinoDim || len(p.b) != EmbedDim {
		return nil, fmt.Errorf("horseid: проекция не загружена")
	}
	e := make([]float64, EmbedDim)
	for i := range e {
		row := p.w[i*DinoDim : (i+1)*DinoDim]
		var sum float64
		for j, v := range row {
			sum += float64(v) * float64(x[j])
		}
		e[i] = sum + float64(p.b[i])
	}
	if err := l2Normalize(e); err != nil {
		return nil, err
	}
	return e, nil
}

package yolo

import (
	"image"
)

// embedGrid — сетка пуллинга эмбеддинга (grid x grid средних RGB).
const embedGrid = 4

// poolEmbedding — детерминированный вектор из пуллинга пикселей кропа:
// grid x grid средних RGB (4x4x3 = 48) + глобальные средние (3) + суммы
// по каналам (3) + нормированные значения до 64. Одинаковый кроп — одинаковый
// вектор для версии модели; НЕ вектор нейросети.
func poolEmbedding(img image.Image) []float32 {
	if img == nil {
		return make([]float32, 64)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return make([]float32, 64)
	}
	cells := make([]bandMean, embedGrid*embedGrid)
	counts := make([]int, embedGrid*embedGrid)
	var sums [3]float64
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		cy := (y - b.Min.Y) * embedGrid / h
		if cy >= embedGrid {
			cy = embedGrid - 1
		}
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			r8, g8, b8 := float64(r>>8), float64(g>>8), float64(bl>>8)
			m := &cells[cy*embedGrid+(x-b.Min.X)*embedGrid/w]
			m.r += r8
			m.g += g8
			m.b += b8
			counts[cy*embedGrid+(x-b.Min.X)*embedGrid/w]++
			sums[0] += r8
			sums[1] += g8
			sums[2] += b8
			n++
		}
	}
	vector := make([]float32, 0, 64)
	for i, c := range cells {
		if counts[i] == 0 {
			continue
		}
		k := float64(counts[i])
		vector = append(vector, float32(c.r/k), float32(c.g/k), float32(c.b/k))
	}
	// глобальные средние
	if n > 0 {
		vector = append(vector, float32(sums[0]/float64(n)), float32(sums[1]/float64(n)), float32(sums[2]/float64(n)))
	}
	// дополнение до 64: детерминированная функция уже собранных значений
	i := 0
	for len(vector) < 64 {
		j := i % len(vector)
		vector = append(vector, vector[j]/float32(1+i))
		i++
	}
	return vector[:64]
}

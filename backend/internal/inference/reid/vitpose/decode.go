package vitpose

import (
	"fmt"
	"math"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid/horseid"
)

// Параметры DARK-уточнения из post_process_pose_estimation (kernel_size=11 -> radius 5).
const (
	gaussSigma  = 0.8
	gaussRadius = 5
	float32Eps  = 1.1920929e-07 // np.finfo(np.float32).eps
)

// gaussianKernel1D — нормированное гауссово ядро длины 2*radius+1 (sigma=0.8),
// как _gaussian_kernel1d в scipy.ndimage (order=0).
func gaussianKernel1D() []float64 {
	k := make([]float64, 2*gaussRadius+1)
	var sum float64
	for i := range k {
		x := float64(i - gaussRadius)
		k[i] = math.Exp(-0.5 * x * x / (gaussSigma * gaussSigma))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	return k
}

// reflectIndex повторяет граничный режим scipy 'reflect' (== numpy 'symmetric'):
// продолжение зеркалит массив без повтора крайнего элемента.
func reflectIndex(i, n int) int {
	if n == 1 {
		return 0
	}
	period := 2 * n
	i = ((i % period) + period) % period
	if i >= n {
		i = period - 1 - i
	}
	return i
}

// gaussianFilter2D — раздельная гауссова фильтрация (sigma=0.8, radius=5, граница
// 'reflect'), эквивалент scipy.ndimage.gaussian_filter(heatmap, sigma=0.8,
// radius=(5,5), axes=(0,1)). Сначала по строкам, затем по столбцам.
func gaussianFilter2D(src []float64, w, h int) []float64 {
	k := gaussianKernel1D()
	tmp := make([]float64, len(src))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			var acc float64
			for t := -gaussRadius; t <= gaussRadius; t++ {
				acc += k[t+gaussRadius] * src[reflectIndex(y+t, h)*w+x]
			}
			tmp[y*w+x] = acc
		}
	}
	out := make([]float64, len(src))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var acc float64
			for t := -gaussRadius; t <= gaussRadius; t++ {
				acc += k[t+gaussRadius] * tmp[y*w+reflectIndex(x+t, w)]
			}
			out[y*w+x] = acc
		}
	}
	return out
}

// darkRefine уточняет целочисленный argmax (ax, ay) до субпиксельной точности методом
// DARK (post_dark_unbiased_data_processing, kernel=11): гауссово сглаживание, clip
// [0.001, 50], логарифм, обращение гессиана в окне 3x3, шаг Ньютона. Для симметричного
// пика почти не смещает точку; для реальных тепмап сдвиг субпиксельный.
func darkRefine(heat []float64, w, h, ax, ay int) (float64, float64) {
	f := gaussianFilter2D(heat, w, h)
	logv := make([]float64, len(f))
	for i, v := range f {
		if v < 0.001 {
			v = 0.001
		} else if v > 50 {
			v = 50
		}
		logv[i] = math.Log(v)
	}
	// Краевое (edge) дополнение на 1 пиксель, как np.pad(..., mode="edge").
	at := func(row, col int) float64 {
		if row < 0 {
			row = 0
		} else if row >= h {
			row = h - 1
		}
		if col < 0 {
			col = 0
		} else if col >= w {
			col = w - 1
		}
		return logv[row*w+col]
	}
	i0 := at(ay, ax)
	ix1 := at(ay, ax+1)
	iy1 := at(ay+1, ax)
	ix1y1 := at(ay+1, ax+1)
	ix1y1_ := at(ay-1, ax-1)
	ix1_ := at(ay, ax-1)
	iy1_ := at(ay-1, ax)
	dx := 0.5 * (ix1 - ix1_)
	dy := 0.5 * (iy1 - iy1_)
	dxx := ix1 - 2*i0 + ix1_
	dyy := iy1 - 2*i0 + iy1_
	dxy := 0.5 * (ix1y1 - ix1 - iy1 + i0 + i0 - ix1_ - iy1_ + ix1y1_)
	hh := dxx + float32Eps
	vv := dyy + float32Eps
	det := hh*vv - dxy*dxy
	if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) {
		return float64(ax), float64(ay)
	}
	rx := float64(ax) - (vv*dx-dxy*dy)/det
	ry := float64(ay) - (hh*dy-dxy*dx)/det
	if math.IsNaN(rx) || math.IsInf(rx, 0) || math.IsNaN(ry) || math.IsInf(ry, 0) {
		return float64(ax), float64(ay)
	}
	return rx, ry
}

// transformPreds переносит точку тепмапы в пиксели исходного фото (transform_preds):
// масштаб восстанавливается умножением на normalize_factor, используется «unbiased»
// диапазон [0, side-1]. Арифметика во float32, как в numpy (coords/center/scale float32).
func transformPreds(px, py float64, center, scale [2]float32, w, h int) (float64, float64) {
	sx := scale[0] * normalizeFactor
	sy := scale[1] * normalizeFactor
	scaleY := sy / float32(h-1)
	scaleX := sx / float32(w-1)
	x := float32(px)*scaleX + center[0] - sx*0.5
	y := float32(py)*scaleY + center[1] - sy*0.5
	return float64(x), float64(y)
}

// decode превращает тепмапы (NumKeypoints, HeatmapHeight, HeatmapWidth) в 17 точек
// в координатах исходного (уменьшенного) фото. box задаётся как [x0,y0,x1,y1].
// Неверная длина буфера — ошибка, а не паника на индексации: форму выхода ONNX-графа
// нельзя считать гарантированной (модель могли подложить другую).
func decode(heatmaps []float32, box [4]float64) ([]horseid.KeyPoint, error) {
	want := NumKeypoints * HeatmapHeight * HeatmapWidth
	if len(heatmaps) != want {
		return nil, fmt.Errorf("vitpose: неверная длина тепмап %d, ожидалось %d (%dx%dx%d)",
			len(heatmaps), want, NumKeypoints, HeatmapHeight, HeatmapWidth)
	}
	center, scale := boxToCenterAndScale(box[0], box[1], box[2]-box[0], box[3]-box[1])
	plane := HeatmapHeight * HeatmapWidth
	kps := make([]horseid.KeyPoint, NumKeypoints)
	heat := make([]float64, plane)
	for kp := 0; kp < NumKeypoints; kp++ {
		base := kp * plane
		best, bestV := 0, heatmaps[base]
		for i := 1; i < plane; i++ {
			if heatmaps[base+i] > bestV {
				bestV = heatmaps[base+i]
				best = i
			}
		}
		for i := 0; i < plane; i++ {
			heat[i] = float64(heatmaps[base+i])
		}
		score := float64(bestV)
		ax, ay := best%HeatmapWidth, best/HeatmapWidth
		rx, ry := float64(ax), float64(ay)
		if score > 0 {
			rx, ry = darkRefine(heat, HeatmapWidth, HeatmapHeight, ax, ay)
		}
		X, Y := transformPreds(rx, ry, center, scale, HeatmapWidth, HeatmapHeight)
		kps[kp] = horseid.KeyPoint{X: X, Y: Y, Score: score}
	}
	return kps, nil
}

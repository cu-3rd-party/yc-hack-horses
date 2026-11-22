package vitpose

import (
	"image"
	"math"
)

// rgbBuffer извлекает каналы RGB изображения в плоский буфер (row-major, 3 байта на
// пиксель). Пиксели берутся из прямого канала (изображение непрозрачное, как после
// PIL convert("RGB")); прозрачность в пайплайне не используется.
func rgbBuffer(img image.Image) ([]uint8, int, int) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	buf := make([]uint8, w*h*3)
	switch src := img.(type) {
	case *image.RGBA:
		for y := 0; y < h; y++ {
			row := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < w; x++ {
				i := (y*w + x) * 3
				buf[i], buf[i+1], buf[i+2] = row[x*4], row[x*4+1], row[x*4+2]
			}
		}
	case *image.NRGBA:
		for y := 0; y < h; y++ {
			row := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < w; x++ {
				i := (y*w + x) * 3
				buf[i], buf[i+1], buf[i+2] = row[x*4], row[x*4+1], row[x*4+2]
			}
		}
	default:
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				i := (y*w + x) * 3
				buf[i], buf[i+1], buf[i+2] = uint8(r>>8), uint8(g>>8), uint8(bl>>8)
			}
		}
	}
	return buf, w, h
}

// sampleRGB билинейно интерполирует буфер в точке (sx, sy) с соглашением
// scipy.ndimage.affine_transform(order=1, mode='constant', cval=0.0), который и вызывает
// VitPoseImageProcessor: за краем массива интерполяция НЕ выполняется, поэтому любая
// координата вне [0, side-1] — включая полосы (-1,0) и (n-1,n) — даёт нулевой пиксель
// целиком, а не смесь крайнего отсчёта с cval. Зеркальная семантика
// (mode='mirror': -1 -> 1, n -> n-2, ноль лишь при cc около нуля) относится
// к другому режиму границы и здесь не применяется:
// сверено побитово с реальным эталоном (transformers 5.24.0, scipy 1.18.1), зеркальный
// вариант даёт расхождение до 4.4 на граничных кропах. Возвращает каналы до квантования.
func sampleRGB(buf []uint8, w, h int, sx, sy float64) (rgb [3]float64) {
	// Условие записано отрицанием, чтобы тем же путём отсечь NaN/±Inf: scipy на такой
	// координате тоже возвращает cval (0), а int(math.Floor(NaN)) дал бы мусорный индекс.
	if !(sx >= 0 && sx <= float64(w-1) && sy >= 0 && sy <= float64(h-1)) {
		return rgb
	}
	x0 := int(math.Floor(sx))
	y0 := int(math.Floor(sy))
	fx := sx - float64(x0)
	fy := sy - float64(y0)
	x1 := min(x0+1, w-1)
	y1 := min(y0+1, h-1)
	p00 := (y0*w + x0) * 3
	p01 := (y0*w + x1) * 3
	p10 := (y1*w + x0) * 3
	p11 := (y1*w + x1) * 3
	w00 := (1 - fx) * (1 - fy)
	w01 := fx * (1 - fy)
	w10 := (1 - fx) * fy
	w11 := fx * fy
	for c := 0; c < 3; c++ {
		rgb[c] = w00*float64(buf[p00+c]) + w01*float64(buf[p01+c]) +
			w10*float64(buf[p10+c]) + w11*float64(buf[p11+c])
	}
	return rgb
}

// quantizeUint8 повторяет приведение scipy ndimage к uint8 (CASE_INTERP_OUT_UINT):
// значение <=0 даёт 0, иначе округление «половина вверх» с зажимом до 255.
func quantizeUint8(v float64) uint8 {
	if v > 0 {
		v += 0.5
	} else {
		v = 0
	}
	if v > 255 {
		v = 255
	}
	if v < 0 {
		v = 0
	}
	return uint8(v)
}

// preprocess выполняет аффинное преобразование бокса box=[x0,y0,x1,y1] (пиксели img)
// в кроп 192x256, затем rescale (÷255) и ImageNet-нормализацию. Возвращает тензор
// NCHW (1,3,256,192) float32. Точный повтор VitPoseImageProcessor.preprocess.
func preprocess(img image.Image, box [4]float64) []float32 {
	buf, w, h := rgbBuffer(img)
	linear, offset := warpPullMatrix(box)
	out := make([]float32, 3*InputHeight*InputWidth)
	for yo := 0; yo < InputHeight; yo++ {
		for xo := 0; xo < InputWidth; xo++ {
			sx := linear[1][0]*float64(yo) + linear[1][1]*float64(xo) + offset[1]
			sy := linear[0][0]*float64(yo) + linear[0][1]*float64(xo) + offset[0]
			rgb := sampleRGB(buf, w, h, sx, sy)
			for c := 0; c < 3; c++ {
				u := quantizeUint8(rgb[c])
				v := (float64(u)*rescaleFactor - float64(imagenetMean[c])) / float64(imagenetStd[c])
				out[(c*InputHeight+yo)*InputWidth+xo] = float32(v)
			}
		}
	}
	return out
}

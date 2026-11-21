package reid

import (
	"image"
	"math"
)

// Separable, antialiased resampling of the detection crop. The two pretrained
// models used PIL bilinear (MegaDescriptor) and bicubic (DINOv2) on RGB bytes.
type kernel struct {
	first   int
	weights []float64
}

func resizeKernels(start, length, output int, cubic bool) []kernel {
	scale := float64(length) / float64(output)
	filterScale := max(1, scale)
	radius := filterScale
	if cubic {
		radius *= 2
	}
	table := make([]kernel, output)
	for i := range table {
		center := (float64(i)+.5)*scale - .5
		lo, hi := max(0, int(math.Ceil(center-radius))), min(length-1, int(math.Floor(center+radius)))
		k := kernel{start + lo, make([]float64, hi-lo+1)}
		var sum float64
		for j := range k.weights {
			x := math.Abs((float64(lo+j) - center) / filterScale)
			w := max(0, 1-x)
			if cubic {
				if x < 1 {
					w = ((1.5*x-2.5)*x)*x + 1
				} else {
					w = ((-0.5*x+2.5)*x-4)*x + 2
				}
			}
			k.weights[j] = w
			sum += w
		}
		for j := range k.weights {
			k.weights[j] /= sum
		}
		table[i] = k
	}
	return table
}

func rgbByte(x float64) uint8 { return uint8(min(255, max(0, math.Round(x)))) }

func tensorPixels(img *image.RGBA, box image.Rectangle, size int, cubic bool) []float32 {
	xs := resizeKernels(box.Min.X, box.Dx(), size, cubic)
	ys := resizeKernels(box.Min.Y, box.Dy(), size, cubic)
	rows := make([]uint8, box.Dy()*size*3)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x, k := range xs {
			var rgb [3]float64
			pos := img.PixOffset(k.first, y)
			for _, w := range k.weights {
				for c := range rgb {
					rgb[c] += w * float64(img.Pix[pos+c])
				}
				pos += 4
			}
			for c, v := range rgb {
				rows[((y-box.Min.Y)*size+x)*3+c] = rgbByte(v)
			}
		}
	}
	pixels := make([]float32, 3*size*size)
	mean := [3]float32{.485, .456, .406}
	std := [3]float32{.229, .224, .225}
	for y, k := range ys {
		for x := range size {
			var rgb [3]float64
			pos := ((k.first-box.Min.Y)*size + x) * 3
			for _, w := range k.weights {
				for c := range rgb {
					rgb[c] += w * float64(rows[pos+c])
				}
				pos += size * 3
			}
			for c, v := range rgb {
				pixels[(c*size+y)*size+x] = (float32(rgbByte(v))/255 - mean[c]) / std[c]
			}
		}
	}
	return pixels
}

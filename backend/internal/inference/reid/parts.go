package reid

import (
	"context"
	"image"
	"math"
)

const (
	grid       = 16
	tokenDim   = 384
	headWidth  = tokenDim + 3 // normalized patch features, x, y and bias
	foreground = .55          // threshold selected on the segmentation validation split
)

type partMaps struct {
	box      image.Rectangle
	channels [4][grid * grid]float64 // silhouette, head, ears, muzzle
}

func decodeParts(tokens, weights []float32, box image.Rectangle) partMaps {
	m := partMaps{box: box}
	for p := range grid * grid {
		v := tokens[(p+1)*tokenDim : (p+2)*tokenDim] // skip CLS
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		scale := 8 / max(1e-7, math.Sqrt(norm))
		for c := range m.channels {
			w := weights[c*headWidth : (c+1)*headWidth]
			var dot float64
			for i, x := range v {
				dot += float64(x) * float64(w[i])
			}
			logit := dot*scale + float64(w[tokenDim])*float64(p%grid)/(grid-1) + float64(w[tokenDim+1])*float64(p/grid)/(grid-1) + float64(w[tokenDim+2])
			m.channels[c][p] = 1 / (1 + math.Exp(-logit))
		}
	}
	return m
}

// Bilinear interpolation of class probabilities before thresholding, in image coordinates.
func (m *partMaps) contains(channel int, x, y int) bool {
	if !image.Pt(x, y).In(m.box) {
		return false
	}
	fx := min(grid-1, max(0, (float64(x-m.box.Min.X)+.5)*grid/float64(m.box.Dx())-.5))
	fy := min(grid-1, max(0, (float64(y-m.box.Min.Y)+.5)*grid/float64(m.box.Dy())-.5))
	i, j := int(fx), int(fy)
	dx, dy := fx-float64(i), fy-float64(j)
	p := &m.channels[channel]
	upper := p[j*grid+i]*(1-dx) + p[j*grid+min(i+1, grid-1)]*dx
	lower := p[min(j+1, grid-1)*grid+i]*(1-dx) + p[min(j+1, grid-1)*grid+min(i+1, grid-1)]*dx
	return upper*(1-dy)+lower*dy >= foreground
}

func pad(box, bounds image.Rectangle, frac float64) image.Rectangle {
	d := image.Pt(max(2, int(float64(box.Dx())*frac)), max(2, int(float64(box.Dy())*frac)))
	return image.Rectangle{Min: box.Min.Sub(d), Max: box.Max.Add(d)}.Intersect(bounds)
}

// Keep the largest 4-connected component, so a neighbouring head cannot expand the detail crop.
func largestComponent(mask []bool, w int) []bool {
	var best []int
	queue := make([]int, 0, len(mask))
	for start, on := range mask {
		if !on {
			continue
		}
		mask[start] = false
		queue = append(queue[:0], start)
		for i := 0; i < len(queue); i++ {
			p := queue[i]
			for _, delta := range [4]image.Point{{X: -1}, {X: 1}, {Y: -1}, {Y: 1}} {
				x, y := p%w+delta.X, p/w+delta.Y
				if x < 0 || x >= w || y < 0 || y >= len(mask)/w {
					continue
				}
				n := y*w + x
				if !mask[n] {
					continue
				}
				mask[n] = false
				queue = append(queue, n)
			}
		}
		if len(queue) > len(best) {
			best = append(best[:0], queue...)
		}
	}
	for _, i := range best {
		mask[i] = true
	}
	return mask
}

type predictParts func(context.Context, image.Rectangle) (partMaps, error)

// sampleAt reads the original image at the centre of a segmentation grid cell.
func sampleAt(img *image.RGBA, box image.Rectangle, i, j, w, h int) (pixel, image.Point) {
	x, y := (2*i+1)*box.Dx()/(2*w), (2*j+1)*box.Dy()/(2*h)
	point := image.Pt(x, y).Add(box.Min)
	c := img.RGBAAt(point.X, point.Y)
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	return pixel{
		x: float64(x), y: float64(y),
		luma:   .2126*r + .7152*g + .0722*b,
		warmth: (r - b) / max(r+g+b, 1./255),
	}, point
}

func segmentedPixels(ctx context.Context, img *image.RGBA, box image.Rectangle, predict predictParts) ([]pixel, error) {
	coarse, err := predict(ctx, pad(box, img.Bounds(), .12))
	if err != nil {
		return nil, err
	}
	w, h := min(128, box.Dx()), min(128, box.Dy())
	samples := make([]pixel, w*h)
	body := make([]bool, len(samples))
	head := make([]bool, len(samples))
	for j := range h {
		for i := range w {
			s, point := sampleAt(img, box, i, j, w, h)
			samples[j*w+i] = s
			body[j*w+i] = coarse.contains(0, point.X, point.Y)
			head[j*w+i] = coarse.contains(1, point.X, point.Y)
		}
	}
	largestComponent(body, w)
	for i := range head {
		head[i] = head[i] && body[i]
	}
	largestComponent(head, w)
	var headBox image.Rectangle
	for i := range samples {
		s := &samples[i]
		s.body, s.head = body[i], head[i]
		if s.head {
			p := box.Min.Add(image.Pt(int(s.x), int(s.y)))
			headBox = headBox.Union(image.Rectangle{Min: p, Max: p.Add(image.Pt(1, 1))})
		}
	}
	if !headBox.Empty() {
		detail, err := predict(ctx, pad(headBox, img.Bounds(), .25))
		if err != nil {
			return nil, err
		}
		for i := range samples {
			s := &samples[i]
			if !s.body {
				continue
			}
			x, y := int(s.x)+box.Min.X, int(s.y)+box.Min.Y
			s.ears, s.muzzle = detail.contains(2, x, y), detail.contains(3, x, y)
		}
	}
	return samples, nil
}

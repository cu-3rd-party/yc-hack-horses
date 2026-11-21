package comparison

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math/bits"
)

// The API carries one polygon per region. Use the largest exterior boundary
// of its measured pixels. Crop corners and convex hulls would cover background.
func maskPolygon(data []byte, crop, frame image.Rectangle) ([]Point, error) {
	if len(data) == 0 {
		return nil, nil
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: decoding mask: %v", ErrModel, err)
	}
	mask, ok := img.(*image.Paletted)
	if !ok || mask.Rect.Empty() {
		return nil, fmt.Errorf("%w: expected indexed mask", ErrModel)
	}
	outline := outerBoundary(mask)
	if len(outline) == 0 {
		return nil, nil
	}
	polygon := make([]Point, 0, len(outline))
	for i, point := range outline {
		a := point.Sub(outline[(i+len(outline)-1)%len(outline)])
		b := outline[(i+1)%len(outline)].Sub(point)
		if a == b { // Skip points in the middle of a straight run.
			continue
		}
		x := float64(crop.Min.X-frame.Min.X) + float64(point.X)*float64(crop.Dx())/float64(mask.Rect.Dx())
		y := float64(crop.Min.Y-frame.Min.Y) + float64(point.Y)*float64(crop.Dy())/float64(mask.Rect.Dy())
		polygon = append(polygon, Point{x / float64(frame.Dx()), y / float64(frame.Dy())})
	}
	return polygon, nil
}

// Trace grid edges with foreground on the right. This preserves concavities,
// separates diagonally touching pixels and gives holes negative signed area.
func outerBoundary(mask *image.Paletted) []image.Point {
	w, h := mask.Rect.Dx(), mask.Rect.Dy()
	stride := w + 1
	edges := make([]byte, stride*(h+1))
	inside := func(x, y int) bool { return image.Pt(x, y).In(mask.Rect) && mask.ColorIndexAt(x, y) != 0 }
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !inside(x, y) {
				continue
			}
			v := y*stride + x
			if !inside(x, y-1) {
				edges[v] |= 1
			}
			if !inside(x+1, y) {
				edges[v+1] |= 2
			}
			if !inside(x, y+1) {
				edges[v+stride+1] |= 4
			}
			if !inside(x-1, y) {
				edges[v+stride] |= 8
			}
		}
	}
	steps := [4]int{1, stride, -1, -stride}
	var largest []image.Point
	maxArea := 0
	for start := range edges {
		for edges[start] != 0 {
			d := bits.TrailingZeros8(edges[start])
			v, area := start, 0
			var ring []image.Point
			for {
				p := image.Pt(v%stride, v/stride)
				ring = append(ring, p)
				edges[v] &^= 1 << d
				v += steps[d]
				area += p.X*(v/stride) - (v%stride)*p.Y
				if v == start {
					break
				}
				for _, turn := range []int{1, 0, 3} {
					next := (d + turn) % 4
					if edges[v]&(1<<next) != 0 {
						d = next
						break
					}
				}
			}
			if area > maxArea {
				maxArea, largest = area, ring
			}
		}
	}
	return largest
}

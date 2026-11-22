package reid

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// Palette indexes preserve all four foreground bits, even when parts overlap.
// The visible colour uses muzzle > ears > head > body. Index zero is transparent.
func maskPNG(samples []pixel, box image.Rectangle) ([]byte, error) {
	colors := []color.NRGBA{{43, 192, 136, 255}, {101, 140, 248, 255}, {255, 187, 61, 255}, {238, 91, 150, 255}}
	palette := make(color.Palette, 16)
	palette[0] = color.Transparent
	for index := 1; index < len(palette); index++ {
		for bit, c := range colors {
			if index&(1<<bit) != 0 {
				palette[index] = c
			}
		}
	}
	mask := image.NewPaletted(image.Rect(0, 0, min(128, box.Dx()), min(128, box.Dy())), palette)
	var seen bool
	for i, s := range samples {
		for bit, on := range [...]bool{s.body, s.head, s.ears, s.muzzle} {
			if on {
				mask.Pix[i] |= 1 << bit
				seen = true
			}
		}
	}
	if !seen {
		return nil, nil
	}
	var buf bytes.Buffer
	err := png.Encode(&buf, mask)
	return buf.Bytes(), err
}

// maskBackground paints an owned RGB crop before resizing it for MegaDescriptor.
// Only the silhouette bit gates the input. Use nearest grid-cell centres in both axes.
func maskBackground(img *image.RGBA, box image.Rectangle, samples []pixel) bool {
	w, h := min(128, box.Dx()), min(128, box.Dy())
	seen := false
	for y := range box.Dy() {
		row := (2*y + 1) * h / (2 * box.Dy()) * w
		for x := range box.Dx() {
			idx := row + (2*x+1)*w/(2*box.Dx())
			if samples[idx].body {
				seen = true
				continue
			}
			i := img.PixOffset(box.Min.X+x, box.Min.Y+y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 127, 127, 127, 255
		}
	}
	return seen
}

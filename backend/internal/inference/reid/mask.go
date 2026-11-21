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

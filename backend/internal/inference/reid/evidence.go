package reid

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

const rawTraitVersion = "segment-measurements-v1"

// TraitEvidence reports unshifted measurements in their native scales.
// Coat lightness and warmth use the same coat layer. Missing coordinates have no layer.
type TraitEvidence struct {
	Version string             `json:"version"`
	Values  map[string]float64 `json:"values"`
	Layers  map[string][]byte  `json:"layers,omitempty"`
}

var traitKeys = [traitCount]string{"coat_light", "coat_warmth", "cheek_light", "muzzle_contrast", "head_ratio"}

var evidencePalette = color.Palette{
	color.Transparent,
	color.NRGBA{R: 46, G: 212, B: 175, A: 255}, // coat
	color.NRGBA{R: 85, G: 171, B: 255, A: 255}, // cheek or projected head
	color.NRGBA{R: 255, G: 138, B: 70, A: 255}, // muzzle
	color.NRGBA{R: 255, G: 216, B: 90, A: 255}, // ears
}

// ExplainMask replays the pixel measurements from the lossless indexed mask saved by
// the segmenter. Each cell in a displayed layer corresponds to the same sample used to measure the region.
func ExplainMask(src image.Image, box image.Rectangle, encoded []byte) (TraitEvidence, error) {
	var e TraitEvidence
	if box.Empty() || !box.In(src.Bounds()) {
		return e, fmt.Errorf("invalid evidence box %v", box)
	}
	decoded, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		return e, err
	}
	mask, ok := decoded.(*image.Paletted)
	w, h := min(128, box.Dx()), min(128, box.Dy())
	if !ok || mask.Bounds() != image.Rect(0, 0, w, h) || len(mask.Palette) != 16 {
		return e, fmt.Errorf("expected an indexed anatomical mask for box %v", box)
	}
	img := image.NewRGBA(box)
	draw.Draw(img, box, src, box.Min, draw.Src)
	samples := make([]pixel, w*h)
	for j := range h {
		for i := range w {
			p, _ := sampleAt(img, box, i, j, w, h)
			bits := mask.ColorIndexAt(i, j)
			p.body, p.head, p.ears, p.muzzle = bits&1 != 0, bits&2 != 0, bits&4 != 0, bits&8 != 0
			samples[j*w+i] = p
		}
	}
	return evidence(samples, box, true)
}

func evidence(samples []pixel, box image.Rectangle, withLayers bool) (TraitEvidence, error) {
	e := TraitEvidence{Version: rawTraitVersion, Values: make(map[string]float64)}
	bounds := image.Rect(0, 0, min(128, box.Dx()), min(128, box.Dy()))
	width, height := float64(box.Dx()), float64(box.Dy())
	traits := measurements(samples, width, height)
	layers := make(map[string]*image.Paletted)
	for i, t := range traits {
		if !t.visible {
			continue
		}
		e.Values[traitKeys[i]] = t.value
		if withLayers && i != 1 { // warmth shares the coat lightness footprint
			layers[traitKeys[i]] = image.NewPaletted(bounds, evidencePalette)
		}
	}
	if len(layers) == 0 {
		return e, nil
	}
	e.Layers = make(map[string][]byte)
	for i, p := range samples {
		switch colorRegion(p, width, height) {
		case coatRegion:
			if m := layers["coat_light"]; m != nil {
				m.Pix[i] = 1
			}
		case cheekRegion:
			if m := layers["cheek_light"]; m != nil {
				m.Pix[i] = 2
			}
			if m := layers["muzzle_contrast"]; m != nil {
				m.Pix[i] = 2
			}
		case muzzleRegion:
			if m := layers["muzzle_contrast"]; m != nil {
				m.Pix[i] = 3
			}
		}
		if m := layers["head_ratio"]; m != nil && p.body {
			// The ears and muzzle gate the pose. The ratio uses head pixels excluding ears.
			switch {
			case p.ears:
				m.Pix[i] = 4
			case p.muzzle:
				m.Pix[i] = 3
			case p.head:
				m.Pix[i] = 2
			}
		}
	}
	for key, layer := range layers {
		var buf bytes.Buffer
		if err := png.Encode(&buf, layer); err != nil {
			return e, err
		}
		e.Layers[key] = buf.Bytes()
	}
	return e, nil
}

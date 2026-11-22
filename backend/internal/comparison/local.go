package comparison

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"maps"
	"math"
	"slices"
	"strconv"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/yolo"
	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

type InstanceDetector interface {
	EmbedInstances(context.Context, inference.ImageInput, *image.Rectangle) ([]yolo.InstanceEmbedding, error)
}

type LocalModel struct{ Detector InstanceDetector }

var ErrLocalSegmentation = fmt.Errorf("%w: local segmentation is unavailable", ErrUnavailable)

func (m LocalModel) Analyze(ctx context.Context, data []byte, _ string) ([]Prediction, error) {
	// Keep detection, crop masks and the Electron preview in the same image axes.
	img, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", inference.ErrInvalidImage, err)
	}
	rows, err := m.Detector.EmbedInstances(ctx, inference.ImageInput{Decoded: img}, nil)
	switch {
	case errors.Is(err, inference.ErrNoAnimalDetected):
		return []Prediction{}, nil
	case err != nil:
		return nil, err
	}
	out := make([]Prediction, 0, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, err := localPrediction(row, img.Bounds())
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func localPrediction(row yolo.InstanceEmbedding, frame image.Rectangle) (Prediction, error) {
	if row.Box.Empty() || !row.Box.In(frame) || !(row.Confidence >= 0 && row.Confidence <= 1) {
		return Prediction{}, ErrModel
	}
	if row.MeasurementError != "" && row.Embedding.ModelVersion != reid.BoxVersion {
		return Prediction{}, fmt.Errorf("%w: %s", ErrLocalSegmentation, row.MeasurementError)
	}
	annotation, err := AnnotationFromMask(row.MaskPNG, row.Traits, row.Box, frame)
	if errors.Is(err, ErrLocalSegmentation) && row.Embedding.ModelVersion == reid.BoxVersion {
		left := float64(row.Box.Min.X-frame.Min.X) / float64(frame.Dx())
		top := float64(row.Box.Min.Y-frame.Min.Y) / float64(frame.Dy())
		right := float64(row.Box.Max.X-frame.Min.X) / float64(frame.Dx())
		bottom := float64(row.Box.Max.Y-frame.Min.Y) / float64(frame.Dy())
		annotation = Annotation{HorsePolygon: []Point{{left, top}, {right, top}, {right, bottom}, {left, bottom}}, Traits: []Trait{}}
	} else if err != nil {
		return Prediction{}, err
	}
	p := Prediction{
		Confidence: row.Confidence,
		Annotation: annotation,
		Descriptor: Descriptor{Version: row.Embedding.ModelVersion, Vector: make([]float64, len(row.Embedding.Vector))},
	}
	for i, v := range row.Embedding.Vector {
		p.Vector[i] = float64(v)
	}
	if p.Version == "" || unit(p.Vector) == nil {
		return Prediction{}, ErrModel
	}
	return p, nil
}

// AnnotationFromMask places an instance's anatomical raster and measurement polygons
// in the same oriented photo frame, for both uploads and saved reference crops.
func AnnotationFromMask(mask []byte, evidence *reid.TraitEvidence, box, frame image.Rectangle) (Annotation, error) {
	if box.Empty() || !box.In(frame) {
		return Annotation{}, ErrModel
	}
	outline, err := maskPolygon(mask, box, frame)
	if err != nil {
		return Annotation{}, err
	}
	if len(outline) == 0 {
		return Annotation{}, ErrLocalSegmentation
	}
	x := func(px int) float64 { return float64(px-frame.Min.X) / float64(frame.Dx()) }
	y := func(py int) float64 { return float64(py-frame.Min.Y) / float64(frame.Dy()) }
	annotation := Annotation{HorsePolygon: outline, Traits: []Trait{}, PartMask: &PartMask{PNG: mask, Box: [4]float64{x(box.Min.X), y(box.Min.Y), x(box.Max.X), y(box.Max.Y)}}}
	if evidence == nil {
		return annotation, nil
	}
	for _, key := range slices.Sorted(maps.Keys(evidence.Values)) {
		value := evidence.Values[key]
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return Annotation{}, ErrModel
		}
		layer := evidence.Layers[key]
		if key == "coat_warmth" {
			layer = evidence.Layers["coat_light"]
		}
		region, err := maskPolygon(layer, box, frame)
		if err != nil {
			return Annotation{}, err
		}
		annotation.Traits = append(annotation.Traits, Trait{Key: key, Value: strconv.FormatFloat(value, 'g', -1, 64), Polygon: region})
	}
	return annotation, nil
}

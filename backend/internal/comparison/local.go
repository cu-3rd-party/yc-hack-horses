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
	if row.MeasurementError != "" {
		return Prediction{}, fmt.Errorf("%w: %s", ErrLocalSegmentation, row.MeasurementError)
	}
	outline, err := maskPolygon(row.MaskPNG, row.Box, frame)
	if err != nil {
		return Prediction{}, err
	}
	if len(outline) == 0 {
		return Prediction{}, ErrLocalSegmentation
	}

	p := Prediction{
		Confidence: row.Confidence,
		Annotation: Annotation{HorsePolygon: outline, Traits: []Trait{}},
		Descriptor: Descriptor{Version: row.Embedding.ModelVersion, Vector: make([]float64, len(row.Embedding.Vector))},
	}
	for i, v := range row.Embedding.Vector {
		p.Vector[i] = float64(v)
	}
	if p.Version == "" || unit(p.Vector) == nil {
		return Prediction{}, ErrModel
	}
	if row.Traits == nil {
		return p, nil
	}
	for _, key := range slices.Sorted(maps.Keys(row.Traits.Values)) {
		value := row.Traits.Values[key]
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return Prediction{}, ErrModel
		}
		layer := row.Traits.Layers[key]
		if key == "coat_warmth" {
			layer = row.Traits.Layers["coat_light"]
		}
		region, err := maskPolygon(layer, row.Box, frame)
		if err != nil {
			return Prediction{}, err
		}
		p.Annotation.Traits = append(p.Annotation.Traits, Trait{Key: key, Value: strconv.FormatFloat(value, 'g', -1, 64), Polygon: region})
	}
	return p, nil
}

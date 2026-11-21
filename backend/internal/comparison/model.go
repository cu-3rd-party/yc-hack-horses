package comparison

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

var ErrUnavailable = errors.New("comparison model with horse segmentation is unavailable")
var ErrModel = errors.New("invalid comparison model response")

type Analyzer interface {
	Analyze(context.Context, []byte, string) ([]Prediction, error)
}

// ModelEndpoint receives the original image bytes. Geometry is in pixels in
// the oriented image frame (width x height), independently of model tensor size.
type ModelEndpoint struct{ URL string }
type Prediction struct {
	Confidence float64
	Annotation Annotation
	Descriptor
}
type modelOutput struct {
	Version    string `json:"model_version"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Detections []struct {
		Confidence *float64    `json:"detection_confidence"`
		Horse      [][]float64 `json:"horse_polygon"`
		Traits     []struct {
			Key     string      `json:"key"`
			Value   string      `json:"value"`
			Polygon [][]float64 `json:"polygon"`
		} `json:"traits"`
		Vector []float64 `json:"embedding"`
	} `json:"detections"`
}

func (m ModelEndpoint) Analyze(ctx context.Context, image []byte, mime string) ([]Prediction, error) {
	if m.URL == "" {
		return nil, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.URL, bytes.NewReader(image))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", mime)
	client := &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, ErrUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: model returned %s", ErrModel, resp.Status)
	}
	const limit = 32 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(data) > limit {
		return nil, ErrModel
	}
	var raw modelOutput
	if json.Unmarshal(data, &raw) != nil || raw.Detections == nil {
		return nil, ErrModel
	}
	out := make([]Prediction, 0, len(raw.Detections))
	for _, d := range raw.Detections {
		if len(d.Horse) == 0 {
			return nil, ErrUnavailable
		}
		if raw.Version == "" || raw.Width < 1 || raw.Height < 1 || d.Confidence == nil || *d.Confidence < 0 || *d.Confidence > 1 || unit(d.Vector) == nil {
			return nil, ErrModel
		}
		horse, err := raw.polygon(d.Horse)
		if err != nil {
			return nil, err
		}
		a := Annotation{HorsePolygon: horse, Traits: make([]Trait, 0, len(d.Traits))}
		for _, t := range d.Traits {
			if t.Key == "" || t.Value == "" {
				return nil, ErrModel
			}
			var mask []Point
			if t.Polygon != nil {
				mask, err = raw.polygon(t.Polygon)
				if err != nil {
					return nil, err
				}
			}
			a.Traits = append(a.Traits, Trait{Key: t.Key, Value: t.Value, Polygon: mask})
		}
		out = append(out, Prediction{Confidence: *d.Confidence, Annotation: a, Descriptor: Descriptor{Version: raw.Version, Vector: d.Vector}})
	}
	return out, nil
}

func (m modelOutput) polygon(points [][]float64) ([]Point, error) {
	if len(points) < 3 {
		return nil, ErrModel
	}
	p := make([]Point, len(points))
	distinct := make(map[Point]struct{}, 3)
	for i, v := range points {
		if len(v) != 2 {
			return nil, ErrModel
		}
		p[i] = Point{v[0] / float64(m.Width), v[1] / float64(m.Height)}
		for _, n := range p[i] {
			if n < 0 || n > 1 || math.IsNaN(n) {
				return nil, ErrModel
			}
		}
		if len(distinct) < 3 {
			distinct[p[i]] = struct{}{}
		}
	}
	if len(distinct) < 3 {
		return nil, ErrModel
	}
	return p, nil
}

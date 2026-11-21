package reid

import (
	"fmt"
	"math"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

const (
	Version       = "megadescriptor-l384-visual-v1"
	Dimension     = 1536
	legacyVersion = "megadescriptor-l384-pascal-parts-physical-v1"
)

func visualEmbedding(values []float32) (inference.EmbeddingResult, error) {
	if len(values) != Dimension {
		return inference.EmbeddingResult{}, fmt.Errorf("reid: expected %d visual coordinates", Dimension)
	}
	var squared float64
	for _, x := range values {
		squared += float64(x) * float64(x)
	}
	if squared == 0 || math.IsNaN(squared) || math.IsInf(squared, 0) {
		return inference.EmbeddingResult{}, fmt.Errorf("reid: invalid visual vector")
	}
	norm := math.Sqrt(squared)
	for i, x := range values {
		values[i] = float32(float64(x) / norm)
	}
	return inference.EmbeddingResult{ModelVersion: Version, Vector: values}, nil
}

// Old exports store the same visual block with a fixed scale, followed by ten trait coordinates.
func squaredVisualNorm(e inference.EmbeddingResult) (float64, error) {
	switch {
	case e.ModelVersion == Version && len(e.Vector) == Dimension:
		return 1, nil
	case e.ModelVersion == legacyVersion && len(e.Vector) == Dimension+10:
		return .9, nil
	default:
		return 0, fmt.Errorf("reid: incompatible embedding version or dimensions")
	}
}

// Validate checks the part used for retrieval once on import.
func Validate(e inference.EmbeddingResult) error {
	want, err := squaredVisualNorm(e)
	if err != nil {
		return err
	}
	var sum float64
	for _, x := range e.Vector[:Dimension] {
		sum += float64(x) * float64(x)
	}
	if !(math.Abs(sum-want) < 1e-5) {
		return fmt.Errorf("reid: invalid visual norm")
	}
	return nil
}

// Similarity is the visual cosine for both new and archived descriptors.
func Similarity(a, b inference.EmbeddingResult) (float64, error) {
	na, err := squaredVisualNorm(a)
	if err != nil {
		return 0, err
	}
	nb, err := squaredVisualNorm(b)
	if err != nil {
		return 0, err
	}
	var dot float64
	for i := range Dimension {
		dot += float64(a.Vector[i]) * float64(b.Vector[i])
	}
	score := dot / math.Sqrt(na*nb)
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0, fmt.Errorf("reid: invalid cosine")
	}
	return min(1, max(-1, score)), nil
}

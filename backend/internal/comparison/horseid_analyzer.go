// Адаптер horseid-пайплайна к comparison (оркестратор, интеграция координатора).
// Лежит в пакете comparison, чтобы comparison импортировал horseid (и не наоборот).
package comparison

import (
	"context"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid/horseid"
)

// HorseAnalyzer реализует Analyzer поверх horseid-пайплайна: один Prediction
// на найденную лошадь, вектор длины 512 — concat(head256, body256).
// При отсутствии головы head-половина нулевая (договорённость horseid_rank.go:
// нулевая половина = части нет).
type HorseAnalyzer struct {
	pipeline horseid.Pipeline
}

// NewHorseAnalyzer собирает адаптер из детектора боксов YOLO, оценщика позы
// ViTPose и эмбеддера DINOv2+ArcFace.
func NewHorseAnalyzer(boxes horseid.BoxDetector, pose horseid.PoseEstimator, embed horseid.Embedder) HorseAnalyzer {
	return HorseAnalyzer{pipeline: horseid.New(boxes, pose, embed)}
}

// Analyze реализует Analyzer. Контекст и mime не используются: пайплайн
// работает локально по байтам изображения. Если лошадь не найдена —
// inference.ErrNoAnimalDetected (существующая семантика).
func (a HorseAnalyzer) Analyze(_ context.Context, imageBytes []byte, _ string) ([]Prediction, error) {
	res, err := a.pipeline.Analyze(imageBytes)
	if err != nil {
		return nil, err
	}
	vector := make([]float64, 2*horseid.EmbedDim)
	copy(vector[:horseid.EmbedDim], res.Embedding.Head)
	copy(vector[horseid.EmbedDim:], res.Embedding.Body)
	return []Prediction{{
		Confidence: res.Confidence,
		Descriptor: Descriptor{Version: horseid.Version, Vector: vector},
	}}, nil
}

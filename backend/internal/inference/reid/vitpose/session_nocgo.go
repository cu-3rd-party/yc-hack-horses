//go:build !cgo

// Fallback без CGO: инференс ViTPose+ требует onnxruntime через cgo, поэтому
// Estimate деградирует в inference.ErrModelUnavailable, а приложение продолжает
// работать (детектор позы становится недоступным, пайплайн обрабатывает это graceful).
package vitpose

import (
	"context"
	"fmt"
	"image"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid/horseid"
)

// Estimator — заглушка: инференсу нужен onnxruntime через cgo.
type Estimator struct {
	runtimeLib string
	modelDir   string
	modelName  string
}

// New — та же сигнатура, что в cgo-варианте.
func New(runtimeLib, modelDir, modelName string) *Estimator {
	return &Estimator{runtimeLib: runtimeLib, modelDir: modelDir, modelName: modelName}
}

// Estimate — деградация в ErrModelUnavailable после проверки входа.
func (e *Estimator) Estimate(ctx context.Context, img image.Image, box [4]float64) ([]horseid.KeyPoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if box[2] <= box[0] || box[3] <= box[1] {
		return nil, fmt.Errorf("%w: некорректный бокс %v", inference.ErrInvalidImage, box)
	}
	return nil, fmt.Errorf("%w: бинарник собран без CGO, ViTPose-инференс недоступен", inference.ErrModelUnavailable)
}

// Close — заглушка.
func (e *Estimator) Close() error { return nil }

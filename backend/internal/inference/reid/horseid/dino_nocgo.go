//go:build !cgo

// Заглушка без CGO: DINOv2-инференс требует onnxruntime через cgo,
// поэтому OpenDino деградирует в inference.ErrModelUnavailable.
package horseid

import (
	"context"
	"fmt"
	"image"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

// Dino — заглушка: сессии нет.
type Dino struct{}

// OpenDino всегда возвращает ErrModelUnavailable.
func OpenDino(modelPath string) (*Dino, error) {
	return nil, fmt.Errorf("%w: бинарник собран без CGO, DINOv2 недоступен", inference.ErrModelUnavailable)
}

// Embed — деградация в ErrModelUnavailable.
func (d *Dino) Embed(ctx context.Context, img image.Image) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w: бинарник собран без CGO, DINOv2 недоступен", inference.ErrModelUnavailable)
}

// Close — no-op.
func (d *Dino) Close() error { return nil }

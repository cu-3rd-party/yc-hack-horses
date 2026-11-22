//go:build !cgo

// Fallback без CGO (например, distroless-контейнер центральной ноды):
// API движка тот же, но YOLO-инференс недоступен — ExtractTraits/Embed
// деградируют в inference.ErrModelUnavailable, приложение продолжает
// работать (ADR-0012: человек вводит признаки сам).
package yolo

import (
	"context"
	"fmt"
	"image"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
)

// Engine — заглушка: инференсу нужен onnxruntime через cgo.
type Engine struct {
	cfg Config
}

// New — та же сигнатура, что в cgo-варианте.
func New(cfg Config) *Engine {
	return &Engine{cfg: cfg}
}

// SetFusionConfig — та же сигнатура, что в cgo-варианте: инференс без CGO
// недоступен, коэффициенты просто сохраняются для совместимости API.
func (e *Engine) SetFusionConfig(fc *reid.FusionConfig) { e.cfg.FusionConfig = fc }

// FusionConfig — та же сигнатура, что в cgo-варианте.
func (e *Engine) FusionConfig() *reid.FusionConfig { return e.cfg.FusionConfig }

// SetCushkiConfig — та же сигнатура, что в cgo-варианте: инференс без CGO
// недоступен, конфиг сохраняется для совместимости API.
func (e *Engine) SetCushkiConfig(cc *reid.CushkiConfig) { e.cfg.CushkiConfig = cc }

// CushkiConfig — та же сигнатура, что в cgo-варианте.
func (e *Engine) CushkiConfig() *reid.CushkiConfig { return e.cfg.CushkiConfig }

// unavailable — причина деградации: бинарник собран без cgo.
func (e *Engine) unavailable() error {
	return fmt.Errorf("%w: бинарник собран без CGO, YOLO-инференс недоступен (ADR-0012)", inference.ErrModelUnavailable)
}

// ExtractTraits — деградация в ErrModelUnavailable после проверки входа.
func (e *Engine) ExtractTraits(ctx context.Context, input inference.ImageInput) (inference.TraitResult, error) {
	if _, err := decodeImage(ctx, e.cfg.Blobs, input); err != nil {
		return inference.TraitResult{}, err
	}
	return inference.TraitResult{}, e.unavailable()
}

// Embed — деградация в ErrModelUnavailable после проверки входа.
func (e *Engine) Embed(ctx context.Context, input inference.ImageInput) (inference.EmbeddingResult, error) {
	if _, err := decodeImage(ctx, e.cfg.Blobs, input); err != nil {
		return inference.EmbeddingResult{}, err
	}
	return inference.EmbeddingResult{}, e.unavailable()
}

func (e *Engine) EmbedInstances(ctx context.Context, input inference.ImageInput, _ *image.Rectangle) ([]InstanceEmbedding, error) {
	_, err := e.Embed(ctx, input)
	return nil, err
}
func (e *Engine) Close() error { return nil }

func (e *Engine) ModelsChanged(string) {}

const NativeRuntime = false

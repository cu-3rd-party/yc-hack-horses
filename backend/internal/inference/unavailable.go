package inference

import "context"

// UnavailableEngine — заглушка без обученной модели. Всегда возвращает
// ErrModelUnavailable: приложение не блокируется, признаки вводит человек
// (ADR-0012). Используется по умолчанию, если engine не настроен в конфиге.
type UnavailableEngine struct{}

// NewUnavailable — конструктор заглушки.
func NewUnavailable() UnavailableEngine { return UnavailableEngine{} }

// ExtractTraits всегда возвращает ErrModelUnavailable.
func (UnavailableEngine) ExtractTraits(_ context.Context, _ ImageInput) (TraitResult, error) {
	return TraitResult{}, ErrModelUnavailable
}

// Embed всегда возвращает ErrModelUnavailable.
func (UnavailableEngine) Embed(_ context.Context, _ ImageInput) (EmbeddingResult, error) {
	return EmbeddingResult{}, ErrModelUnavailable
}

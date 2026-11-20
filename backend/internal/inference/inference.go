// Package inference: контрактная основа локального CV-inference (ADR-0012).
// V0 — без обученной модели: UnavailableEngine не блокирует приложение
// (человек вводит признаки сам), ReferenceEngine — детерминированный baseline
// на цветовых гистограммах (НЕ обученная модель, НЕ идентификация).
//
// Точка расширения ONNX (V1): подключить onnxruntime-go в реализации
// InferenceEngine — контракты ImageInput/TraitResult/EmbeddingResult
// рассчитаны на подачу тензора после PreprocessSpec (см. ModelMetadata).
package inference

import (
	"context"
	"errors"
	"io"
)

// ErrModelUnavailable — модель недоступна (нет весов, файл не загружен).
// Приложение обязано продолжать работу: признаки вводит человек (ADR-0012).
var ErrModelUnavailable = errors.New("inference: model unavailable")

// ErrInvalidImage — данные не декодируются как изображение или отсутствуют.
var ErrInvalidImage = errors.New("inference: invalid image")

// ErrNoAnimalDetected — детекция не нашла животное на изображении
// (движок детекции YOLO). Приложение продолжает работать: признаки вводит
// человек (ADR-0012).
var ErrNoAnimalDetected = errors.New("inference: no animal detected")

// InferenceEngine — контракт локального CV-движка. Все методы должны быть
// безопасны для вызова без обученной модели: движок-заглушка возвращает
// ErrModelUnavailable, а не панику или блокировку.
type InferenceEngine interface {
	// ExtractTraits — извлечение признаков из изображения по blob sha256.
	ExtractTraits(ctx context.Context, input ImageInput) (TraitResult, error)
	// Embed — векторное представление изображения (для поиска похожих).
	Embed(ctx context.Context, input ImageInput) (EmbeddingResult, error)
}

// ImageInput — вход inference: чтение файла/blob по sha256 через BlobStore.
// Reader используется движками, не умеющими читать BlobStore (тесты, потоки);
// если Reader nil и BlobStore движка не nil, данные читаются из BlobStore.
type ImageInput struct {
	SHA256 string    // sha256 blob'а в BlobStore
	Reader io.Reader // опционально: прямое чтение, BlobStore не нужен
}

// TraitResult — результат ExtractTraits.
type TraitResult struct {
	ModelVersion string            // версия модели/эвристики
	Traits       map[string]string // канонический набор: ключи из CanonicalKeys, значения из допустимых
	Confidence   float64           // 0..1
}

// EmbeddingResult — результат Embed.
type EmbeddingResult struct {
	ModelVersion string
	Vector       []float32
}

// ModelMetadata — описание модели/эвристики: версия, схемы входа и выхода,
// спецификация предобработки. Хранится в коде как константы — единый источник
// истины для desktop/mobile клиентов и будущей ONNX-интеграции.
type ModelMetadata struct {
	Version        string
	InputSchema    string
	OutputSchema   string
	PreprocessSpec PreprocessSpec
}

// PreprocessSpec — предобработка изображения перед inference.
type PreprocessSpec struct {
	TargetWidth  int    // ширина тензора входа
	TargetHeight int    // высота тензора входа
	ColorSpace   string // "rgb" | "gray"
	Layout       string // "nhwc" | "nchw"
	NormalizeMin float64
	NormalizeMax float64
}

// ModelMetadata ReferenceEngine — baseline-эвристика цветовых гистограмм.
// НЕ обученная модель, НЕ идентификация животных.
var referenceMetadata = ModelMetadata{
	Version:      "reference-v1",
	InputSchema:  `{"type":"image","formats":["png","jpeg","gif"],"source":"blob_store","key":"sha256"}`,
	OutputSchema: `{"traits":"map[string]string","confidence":"float64","embedding":"[]float32"}`,
	PreprocessSpec: PreprocessSpec{
		TargetWidth:  256,
		TargetHeight: 256,
		ColorSpace:   "rgb",
		Layout:       "nhwc",
		NormalizeMin: 0,
		NormalizeMax: 1,
	},
}

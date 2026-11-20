// Package yolo: детекция лошади на фото предобученной YOLO ONNX (COCO)
// и предзаполнение трейтов по кропам. ADR-0012 (V1).
//
// Честные границы (ТЗ): YOLO обучен на COCO, НЕ на лошадях Пржевальского.
// Он даёт только прямоугольную область животного; признаки по кропам —
// эвристика-предзаполнение, человек обязан подтвердить (human-in-the-loop,
// trait_snapshots confirmed=false). Предзаполнение — НЕ идентификация.
package yolo

import (
	"context"
	"fmt"
	"image"
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

// ModelVersion — версия связки модель+эвристика. Меняется при смене весов.
const ModelVersion = "yolov8n-coco-v1"

const (
	inputW       = 640 // вход тензора YOLO
	inputH       = 640
	numClasses   = 80 // COCO
	numChannels  = 84 // 4 координаты + 80 классов (YOLOv8 после NMS-free head)
	numAnchors   = 8400
	horseClassID = 17 // COCO class "horse"
	// headFractionPct — голова как верхняя доля bounding box в процентах
	// (эвристика кропа).
	headFractionPct = 30
	// headWidthPct — ширина кропа головы в процентах ширины бокса.
	headWidthPct = 60
	// cropMarginPct — запас вокруг bounding box при кропе (проценты стороны).
	cropMarginPct = 5
)

// Настройки по умолчанию; переопределяются Config.
const (
	DefaultConfThreshold = 0.25
	DefaultIouThreshold  = 0.45
)

// Config — конфигурация YoloEngine.
type Config struct {
	// ModelPath — путь к yolov8n.onnx (вне git, infra/scripts/download-yolo-model.sh).
	ModelPath string
	// RuntimeLibPath — путь к onnxruntime shared library (libonnxruntime.so).
	RuntimeLibPath string
	// ConfThreshold — порог уверенности детекции (0..1).
	ConfThreshold float64
	// IouThreshold — порог IoU для NMS.
	IouThreshold float64
	// Blobs — источник изображений по sha256 (storage.BlobStore); nil = только Reader.
	Blobs inference.BlobFetcher
	// Delegate — стратегия трейтов по кропам; nil = внутренняя гистограммная.
	Delegate TraitDelegate
}

// Engine — InferenceEngine на детекции YOLO. Лениво загружает модель: если
// веса или onnxruntime-библиотека недоступны, ExtractTraits/Embed возвращают
// ошибку inference.ErrModelUnavailable, приложение продолжает работать
// (ADR-0012: человек вводит признаки сам).
type Engine struct {
	cfg Config

	mu      sync.Mutex
	session *ort.DynamicAdvancedSession
	loadErr error
	loaded  bool
}

// New создаёт движок. Загрузка модели отложена до первого вызова.
func New(cfg Config) *Engine {
	if cfg.ConfThreshold <= 0 || cfg.ConfThreshold >= 1 {
		cfg.ConfThreshold = DefaultConfThreshold
	}
	if cfg.IouThreshold <= 0 || cfg.IouThreshold >= 1 {
		cfg.IouThreshold = DefaultIouThreshold
	}
	if cfg.Delegate == nil {
		cfg.Delegate = NewHistogramDelegate()
	}
	return &Engine{cfg: cfg}
}

// ensureSession лениво инициализирует onnxruntime и сессию модели.
// Любая ошибка кэшируется как недоступность модели (не паника, не блокировка).
func (e *Engine) ensureSession() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loaded {
		return e.loadErr
	}
	e.loaded = true
	switch {
	case e.cfg.RuntimeLibPath == "":
		e.loadErr = fmt.Errorf("%w: onnxruntime shared library path not set (inference.runtime_lib)", inference.ErrModelUnavailable)
		return e.loadErr
	case e.cfg.ModelPath == "":
		e.loadErr = fmt.Errorf("%w: model path not set (inference.model_path)", inference.ErrModelUnavailable)
		return e.loadErr
	}
	for _, p := range []string{e.cfg.RuntimeLibPath, e.cfg.ModelPath} {
		if _, err := os.Stat(p); err != nil {
			e.loadErr = fmt.Errorf("%w: %s: %v", inference.ErrModelUnavailable, p, err)
			return e.loadErr
		}
	}
	ort.SetSharedLibraryPath(e.cfg.RuntimeLibPath)
	if !ort.IsInitialized() {
		if err := ort.InitializeEnvironment(); err != nil {
			e.loadErr = fmt.Errorf("%w: init onnxruntime: %v", inference.ErrModelUnavailable, err)
			return e.loadErr
		}
	}
	// YOLOv8 ONNX: вход "images" 1x3x640x640, выход "output0" 1x84x8400.
	session, err := ort.NewDynamicAdvancedSession(e.cfg.ModelPath,
		[]string{"images"}, []string{"output0"}, nil)
	if err != nil {
		e.loadErr = fmt.Errorf("%w: session: %v", inference.ErrModelUnavailable, err)
		return e.loadErr
	}
	e.session = session
	return nil
}

// runInference — letterbox + прогон сессии; возвращает данные выхода
// 1x84x8400 и параметры letterbox для обратного маппинга координат.
func (e *Engine) runInference(img image.Image) ([]float32, letterboxParams, error) {
	lr, err := letterboxTensor(img)
	if err != nil {
		return nil, letterboxParams{}, err
	}
	in, err := ort.NewTensor(ort.NewShape(1, 3, inputW, inputH), lr.data)
	if err != nil {
		return nil, letterboxParams{}, fmt.Errorf("yolo: input tensor: %w", err)
	}
	out, err := ort.NewEmptyTensor[float32](ort.NewShape(1, numChannels, numAnchors))
	if err != nil {
		return nil, letterboxParams{}, fmt.Errorf("yolo: output tensor: %w", err)
	}
	if err := e.session.Run([]ort.Value{in}, []ort.Value{out}); err != nil {
		return nil, letterboxParams{}, fmt.Errorf("yolo: run: %w", err)
	}
	return out.GetData(), lr.params, nil
}

// ExtractTraits: детекция лошади → кропы (корпус, голова) → предзаполнение
// трейтов делегирующей стратегией. Ошибка ErrNoAnimalDetected, если лошадь
// не найдена. Результат всегда требует подтверждения человеком.
func (e *Engine) ExtractTraits(ctx context.Context, input inference.ImageInput) (inference.TraitResult, error) {
	// сначала проверяем вход (дешёвая ошибка), затем модель
	img, err := decodeImage(ctx, e.cfg.Blobs, input)
	if err != nil {
		return inference.TraitResult{}, err
	}
	if err := e.ensureSession(); err != nil {
		return inference.TraitResult{}, err
	}
	out, params, err := e.runInference(img)
	if err != nil {
		return inference.TraitResult{}, err
	}
	dets := decodeOutput(out, horseClassID, e.cfg.ConfThreshold)
	dets = nms(dets, e.cfg.IouThreshold)
	if len(dets) == 0 {
		return inference.TraitResult{}, fmt.Errorf("%w: class %d, conf %.2f", inference.ErrNoAnimalDetected, horseClassID, e.cfg.ConfThreshold)
	}
	// Мультивидные лошади: берём самую уверенную детекцию.
	best := dets[0]
	box := unletterbox(best.Box, params)
	crops := makeCrops(img, box)
	traits, _, err := e.cfg.Delegate.ExtractTraitsFromCrops(ctx, crops)
	if err != nil {
		return inference.TraitResult{}, err
	}
	return inference.TraitResult{
		ModelVersion: ModelVersion,
		Traits:       traits,
		Confidence:   best.Confidence,
	}, nil
}

// Embed — детерминированный вектор из пуллинга пикселей кропа корпуса
// (сетка 4x4 средних RGB + глобальные средние). НЕ вектор нейросети:
// YOLO-детектор не даёт эмбеддингов; используется для поиска похожих снимков
// и детерминирован для версии модели.
func (e *Engine) Embed(ctx context.Context, input inference.ImageInput) (inference.EmbeddingResult, error) {
	img, err := decodeImage(ctx, e.cfg.Blobs, input)
	if err != nil {
		return inference.EmbeddingResult{}, err
	}
	if err := e.ensureSession(); err != nil {
		return inference.EmbeddingResult{}, err
	}
	out, params, err := e.runInference(img)
	if err != nil {
		return inference.EmbeddingResult{}, err
	}
	dets := decodeOutput(out, horseClassID, e.cfg.ConfThreshold)
	dets = nms(dets, e.cfg.IouThreshold)
	if len(dets) == 0 {
		return inference.EmbeddingResult{}, fmt.Errorf("%w: class %d, conf %.2f", inference.ErrNoAnimalDetected, horseClassID, e.cfg.ConfThreshold)
	}
	box := unletterbox(dets[0].Box, params)
	crops := makeCrops(img, box)
	return inference.EmbeddingResult{
		ModelVersion: ModelVersion,
		Vector:       poolEmbedding(crops.Body),
	}, nil
}

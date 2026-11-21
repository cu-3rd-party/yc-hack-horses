// Package yolo: детекция лошади на фото предобученной YOLO ONNX (COCO)
// и предзаполнение трейтов по кропам. ADR-0012 (V1).
//
// Честные границы (ТЗ): YOLO обучен на COCO, НЕ на лошадях Пржевальского.
// Он даёт только прямоугольную область животного; признаки по кропам —
// эвристика-предзаполнение, человек обязан подтвердить (human-in-the-loop,
// trait_snapshots confirmed=false). Предзаполнение — НЕ идентификация.
//
//go:build cgo

package yolo

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
)

// Engine — InferenceEngine на детекции YOLO. Лениво загружает модель: если
// веса или onnxruntime-библиотека недоступны, ExtractTraits/Embed возвращают
// ошибку inference.ErrModelUnavailable, приложение продолжает работать
// (ADR-0012: человек вводит признаки сам).
type Engine struct {
	cfg Config

	life        sync.RWMutex
	reidOnce    sync.Once
	encoder     *reid.Encoder
	encoderErr  error
	segmentOnce sync.Once
	segmenter   *reid.Segmenter
	segmentErr  error
	mu          sync.Mutex
	session     *ort.DynamicAdvancedSession
	loadErr     error
	loaded      bool
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
	if err := initRuntime(e.cfg.RuntimeLibPath); err != nil {
		e.loadErr = fmt.Errorf("%w: init onnxruntime: %v", inference.ErrModelUnavailable, err)
		return e.loadErr
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
	defer in.Destroy()
	out, err := ort.NewEmptyTensor[float32](ort.NewShape(1, numChannels, numAnchors))
	if err != nil {
		return nil, letterboxParams{}, fmt.Errorf("yolo: output tensor: %w", err)
	}
	defer out.Destroy()
	if err := e.session.Run([]ort.Value{in}, []ort.Value{out}); err != nil {
		return nil, letterboxParams{}, fmt.Errorf("yolo: run: %w", err)
	}
	return append([]float32(nil), out.GetData()...), lr.params, nil
}

// ExtractTraits: детекция лошади → кропы (корпус, голова) → предзаполнение
// трейтов делегирующей стратегией. Ошибка ErrNoAnimalDetected, если лошадь
// не найдена. Результат всегда требует подтверждения человеком.
func (e *Engine) ExtractTraits(ctx context.Context, input inference.ImageInput) (inference.TraitResult, error) {
	e.life.RLock()
	defer e.life.RUnlock()
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
	// Для одиночного снимка признаков нужна самая уверенная детекция.
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

// Embed keeps the single-instance engine contract. EmbedInstances processes a whole photo.
func (e *Engine) Embed(ctx context.Context, input inference.ImageInput) (inference.EmbeddingResult, error) {
	rows, err := e.embeddings(ctx, input, nil, true)
	if err != nil {
		return inference.EmbeddingResult{}, err
	}
	return rows[0].Embedding, nil
}

// EmbedInstances embeds each detection separately. A supplied box selects one operator-defined ROI.
func (e *Engine) EmbedInstances(ctx context.Context, input inference.ImageInput, region *image.Rectangle) ([]InstanceEmbedding, error) {
	return e.embeddings(ctx, input, region, false)
}

func (e *Engine) embeddings(ctx context.Context, input inference.ImageInput, region *image.Rectangle, first bool) ([]InstanceEmbedding, error) {
	e.life.RLock()
	defer e.life.RUnlock()
	img, err := decodeImage(ctx, e.cfg.Blobs, input)
	if err != nil {
		return nil, err
	}
	if err = e.ensureSession(); err != nil {
		return nil, err
	}
	var rows []InstanceEmbedding
	if region != nil {
		if region.Empty() || !region.In(img.Bounds()) {
			return nil, fmt.Errorf("%w: box outside image", inference.ErrInvalidImage)
		}
		rows = append(rows, InstanceEmbedding{Box: *region})
	} else {
		out, params, err := e.runInference(img)
		if err != nil {
			return nil, err
		}
		for _, det := range nms(decodeOutput(out, horseClassID, e.cfg.ConfThreshold), e.cfg.IouThreshold) {
			box := unletterbox(det.Box, params)
			if box.Empty() {
				continue
			}
			rows = append(rows, InstanceEmbedding{Box: box, Confidence: det.Confidence})
			if first {
				break
			}
		}
	}
	if len(rows) == 0 {
		return nil, inference.ErrNoAnimalDetected
	}
	if e.cfg.ReIDModelDir != "" {
		e.reidOnce.Do(func() { e.encoder, e.encoderErr = reid.Open(e.cfg.ReIDModelDir) })
		if e.encoderErr != nil {
			return nil, fmt.Errorf("%w: %v", inference.ErrModelUnavailable, e.encoderErr)
		}
	}
	for i := range rows {
		row := &rows[i]
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if e.encoder != nil {
			row.Embedding, err = e.encoder.Embed(ctx, img, row.Box)
			if err != nil {
				return nil, err
			}
			if !first {
				e.measureInstance(ctx, img, row)
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
		} else {
			row.Embedding = inference.EmbeddingResult{ModelVersion: ModelVersion, Vector: poolEmbedding(makeCrops(img, row.Box).Body)}
		}
	}
	return rows, nil
}

// Annotation failures are recorded on the instance and leave its visual prediction usable.
func (e *Engine) measureInstance(ctx context.Context, img image.Image, row *InstanceEmbedding) {
	e.segmentOnce.Do(func() { e.segmenter, e.segmentErr = reid.OpenSegmenter(e.cfg.ReIDModelDir) })
	if e.segmentErr != nil {
		row.MeasurementError = e.segmentErr.Error()
		return
	}
	traits, mask, err := e.segmenter.Measure(ctx, img, row.Box, e.cfg.IncludeMasks)
	if err != nil {
		row.MeasurementError = err.Error()
		return
	}
	row.Traits, row.MaskPNG = &traits, mask
}

// ONNX Runtime is process-global, including when multiple nodes are created in one process.
var runtimeMu sync.Mutex

func initRuntime(path string) error {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if ort.IsInitialized() {
		return nil
	}
	ort.SetSharedLibraryPath(path)
	return ort.InitializeEnvironment()
}

func (e *Engine) Close() error {
	e.life.Lock()
	defer e.life.Unlock()
	var err error
	if e.segmenter != nil {
		err = e.segmenter.Close()
		e.segmenter = nil
	}
	if e.encoder != nil {
		err = errors.Join(err, e.encoder.Close())
		e.encoder = nil
	}
	if e.session != nil {
		err = errors.Join(err, e.session.Destroy())
		e.session = nil
	}
	e.loaded = true
	e.loadErr = inference.ErrModelUnavailable
	return err
}

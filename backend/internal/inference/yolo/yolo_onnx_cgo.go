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
	"path/filepath"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
	"github.com/cu3rd/yc-hack-horses/backend/internal/modelassets"
)

// Engine — InferenceEngine на детекции YOLO. Лениво загружает модель: если
// веса или onnxruntime-библиотека недоступны, ExtractTraits/Embed возвращают
// ошибку inference.ErrModelUnavailable, приложение продолжает работать
// (ADR-0012: человек вводит признаки сам).
type Engine struct {
	cfg Config
	// fusionCfg — внешние коэффициенты скоринга ReID; nil — сырой скор.
	// Читается после New/SetFusionConfig до старта обработки, мутации нет.
	fusionCfg   *reid.FusionConfig
	// cushkiCfg — конфиг CUshki v6; nil — скоринг без калибровки.
	// Читается после New/SetCushkiConfig до старта обработки, мутации нет.
	cushkiCfg   *reid.CushkiConfig

	life        sync.RWMutex
	reidOnce    sync.Once
	encoder     *reid.Encoder
	encoderErr  error
	cushkiOnce   sync.Once
	cushki       *reid.CushkiEncoder
	cushkiErr    error
	segmentOnce sync.Once
	segmenter   *reid.Segmenter
	segmentErr  error
	mu          sync.Mutex
	session     *ort.DynamicAdvancedSession
	version     string
	loadErr     error
	loaded      bool
}

// New создаёт движок. Загрузка модели отложена до первого вызова.
// SetFusionConfig включает внешние коэффициенты скоринга (калибровка,
// веса фьюжна, open-set порог). Вызывать до начала обработки запросов.
func (e *Engine) SetFusionConfig(fc *reid.FusionConfig) { e.fusionCfg = fc }

// FusionConfig возвращает активные коэффициенты (nil — не заданы).
func (e *Engine) FusionConfig() *reid.FusionConfig { return e.fusionCfg }

// SetCushkiConfig включает конфиг CUshki v6 (калибровка матричной головой,
// open-set). Вызывать до начала обработки запросов.
func (e *Engine) SetCushkiConfig(cc *reid.CushkiConfig) { e.cushkiCfg = cc }

// CushkiConfig возвращает активный конфиг CUshki (nil — не задан).
func (e *Engine) CushkiConfig() *reid.CushkiConfig { return e.cushkiCfg }

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
	return &Engine{cfg: cfg, fusionCfg: cfg.FusionConfig, cushkiCfg: cfg.CushkiConfig}
}

// cushkiPaths разбирает список членов ансамбля из ReIDModelPath (";"-разделитель).
func cushkiPaths(path string) []string {
	var out []string
	for _, p := range strings.Split(path, ";") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ensureCushki лениво открывает cushki-энкодер (reid_model_path задан).
func (e *Engine) ensureCushki() error {
	if e.cfg.ReIDModelPath == "" {
		return nil
	}
	e.cushkiOnce.Do(func() {
		paths := cushkiPaths(e.cfg.ReIDModelPath)
		size := e.cfg.ReIDModelInputSize
		if size <= 0 {
			size = 448
		}
		e.cushki, e.cushkiErr = reid.OpenCushkiEncoder(paths, size)
	})
	return e.cushkiErr
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
	session, err := ort.NewDynamicAdvancedSession(e.cfg.ModelPath,
		[]string{"images"}, []string{"output0"}, nil)
	if err != nil {
		e.loadErr = fmt.Errorf("%w: session: %v", inference.ErrModelUnavailable, err)
		return e.loadErr
	}
	e.session = session
	e.version = ModelVersion
	if meta, err := session.GetModelMetadata(); err == nil {
		defer meta.Destroy()
		description, _, _ := meta.LookupCustomMetadataMap("description")
		if strings.Contains(description, "YOLO26x model") {
			e.version = "yolo26x-coco-v1"
		}
	}
	return nil
}

// detect returns filtered horse boxes in model coordinates and the transform back to the photo.
func (e *Engine) detect(img image.Image) ([]Detection, letterboxParams, error) {
	lr, err := letterboxTensor(img)
	if err != nil {
		return nil, letterboxParams{}, err
	}
	in, err := ort.NewTensor(ort.NewShape(1, 3, inputW, inputH), lr.data)
	if err != nil {
		return nil, letterboxParams{}, fmt.Errorf("yolo: input tensor: %w", err)
	}
	defer in.Destroy()
	outputs := make([]ort.Value, 1)
	if err := e.session.Run([]ort.Value{in}, outputs); err != nil {
		return nil, letterboxParams{}, fmt.Errorf("yolo: run: %w", err)
	}
	defer outputs[0].Destroy()
	out, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, lr.params, fmt.Errorf("yolo: expected float32 detector output")
	}
	shape := out.GetShape()
	classes := e.detectionClasses()
	switch {
	case shape.Equals(ort.NewShape(1, numChannels, numAnchors)):
		dets := decodeOutputClasses(out.GetData(), classes, e.cfg.ConfThreshold)
		return nms(dets, e.cfg.IouThreshold), lr.params, nil
	case shape.Equals(ort.NewShape(1, 300, 6)):
		return decodeEndToEndClasses(out.GetData(), classes, e.cfg.ConfThreshold), lr.params, nil
	default:
		return nil, lr.params, fmt.Errorf("yolo: unsupported detector output shape %v", shape)
	}
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
	dets, params, err := e.detect(img)
	if err != nil {
		return inference.TraitResult{}, err
	}
	if len(dets) == 0 {
		return inference.TraitResult{}, fmt.Errorf("%w: classes %v, conf %.2f", inference.ErrNoAnimalDetected, e.detectionClasses(), e.cfg.ConfThreshold)
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
		ModelVersion: e.version,
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
		dets, params, err := e.detect(img)
		if err != nil {
			return nil, err
		}
		for _, det := range dets {
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
	if e.cfg.ReIDModelPath != "" {
		if err = e.ensureCushki(); err != nil {
			return nil, fmt.Errorf("%w: %v", inference.ErrModelUnavailable, err)
		}
	}
	if e.cfg.ReIDModelDir != "" && e.cushki == nil {
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
		if e.cushki != nil {
			var seg *reid.Segmenter
			if e.cfg.ReIDModelDir != "" {
				e.segmentOnce.Do(func() { e.segmenter, e.segmentErr = reid.OpenSegmenter(e.cfg.ReIDModelDir) })
				if e.segmentErr == nil {
					seg = e.segmenter
				}
			}
			row.Embedding, err = e.cushki.Embed(ctx, img, row.Box, seg)
			if err != nil {
				return nil, err
			}
		} else if e.encoder != nil {
			row.Embedding, err = e.encoder.Embed(ctx, img, row.Box)
			if err != nil {
				return nil, err
			}
			if !first || e.cfg.DualVariant {
				e.measureInstance(ctx, img, row)
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
		} else {
			row.Embedding = inference.EmbeddingResult{ModelVersion: e.version, Vector: poolEmbedding(makeCrops(img, row.Box).Body)}
		}
	}
	return rows, nil
}

func (e *Engine) measureInstance(ctx context.Context, img image.Image, row *InstanceEmbedding) {
	// Двухвариантное кодирование: masked-эмбеддинг нужен и первому кропу.
	// Сегментер лениво открывается ниже; при ошибке деградируем к
	// центральному кропу (fallback-версия), не изобретая фильтр фона.
	if e.cfg.DualVariant && e.encoder != nil && row.MaskedEmbedding == nil {
		e.segmentOnce.Do(func() { e.segmenter, e.segmentErr = reid.OpenSegmenter(e.cfg.ReIDModelDir) })
		var masked inference.EmbeddingResult
		var err error
		if e.segmentErr != nil {
			masked, err = e.encoder.EmbedCentral(ctx, img, row.Box)
		} else {
			_, masked, err = e.encoder.EmbedDual(ctx, img, row.Box, e.segmenter)
		}
		if err != nil {
			row.MeasurementError = err.Error()
			return
		}
		row.MaskedEmbedding = &masked
	}
	e.segmentOnce.Do(func() { e.segmenter, e.segmentErr = reid.OpenSegmenter(e.cfg.ReIDModelDir) })
	if e.segmentErr != nil {
		// cushki-энкодер уже учёл fallback маски внутри Embed (центральный
		// кроп, отдельная версия) — не инвалидируем эмбеддинг.
		if e.cushki == nil {
			row.MeasurementError = e.segmentErr.Error()
		}
		return
	}
	region, err := e.segmenter.Region(ctx, img, row.Box, true)
	if err != nil {
		row.MeasurementError = err.Error()
		return
	}
	traits, mask, err := region.Measure(e.cfg.IncludeMasks)
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

// ModelsChanged retries failed lazy loads after an atomic file installation.
func (e *Engine) ModelsChanged(dir string) {
	e.life.Lock()
	defer e.life.Unlock()
	if e.loadErr != nil {
		e.loaded = false
		e.loadErr = nil
	}
	if e.cfg.ReIDModelDir == "" {
		for _, name := range []string{modelassets.DescriptorONNX, modelassets.DescriptorCheckpoint} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				e.cfg.ReIDModelDir = dir
				break
			}
		}
	}
	if e.encoderErr != nil {
		e.reidOnce = sync.Once{}
		e.encoderErr = nil
	}
	if e.segmentErr != nil {
		e.segmentOnce = sync.Once{}
		e.segmentErr = nil
	}
}

const NativeRuntime = true

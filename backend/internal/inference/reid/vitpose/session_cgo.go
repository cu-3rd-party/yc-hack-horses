//go:build cgo

package vitpose

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid/horseid"
)

// Имена тензоров ONNX-модели ViTPose+ (dataset_index=3 запечён при экспорте, M1).
const (
	inputName  = "pixel_values"
	outputName = "heatmaps"
)

// Estimator — оценщик позы ViTPose+ base поверх onnxruntime. Загрузка модели ленива:
// при отсутствии библиотеки/файла Estimate возвращает inference.ErrModelUnavailable
// (приложение продолжает работать, детектор деградирует).
type Estimator struct {
	runtimeLib string
	modelDir   string
	modelName  string

	mu      sync.Mutex
	session *ort.DynamicAdvancedSession
	loaded  bool
	loadErr error
}

// New создаёт оценщик. runtimeLib — путь к разделяемой библиотеке onnxruntime;
// modelDir и modelName — каталог и имя файла ONNX-модели (их подкладывает модуль M8).
// Ничего не загружает до первого вызова Estimate.
func New(runtimeLib, modelDir, modelName string) *Estimator {
	return &Estimator{runtimeLib: runtimeLib, modelDir: modelDir, modelName: modelName}
}

// ensureSession лениво инициализирует onnxruntime и сессию. Любая ошибка кэшируется
// как недоступность модели.
func (e *Estimator) ensureSession() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loaded {
		return e.loadErr
	}
	e.loaded = true
	switch {
	case e.runtimeLib == "":
		e.loadErr = fmt.Errorf("%w: путь к onnxruntime не задан", inference.ErrModelUnavailable)
		return e.loadErr
	case e.modelName == "":
		e.loadErr = fmt.Errorf("%w: имя ONNX-модели не задано", inference.ErrModelUnavailable)
		return e.loadErr
	}
	path := filepath.Join(e.modelDir, e.modelName)
	for _, p := range []string{e.runtimeLib, path} {
		if _, err := os.Stat(p); err != nil {
			e.loadErr = fmt.Errorf("%w: %s: %v", inference.ErrModelUnavailable, p, err)
			return e.loadErr
		}
	}
	if err := initRuntime(e.runtimeLib); err != nil {
		e.loadErr = fmt.Errorf("%w: init onnxruntime: %v", inference.ErrModelUnavailable, err)
		return e.loadErr
	}
	opts, err := ort.NewSessionOptions()
	if err != nil {
		e.loadErr = fmt.Errorf("%w: session options: %v", inference.ErrModelUnavailable, err)
		return e.loadErr
	}
	defer opts.Destroy()
	_ = opts.SetIntraOpNumThreads(4)
	_ = opts.SetInterOpNumThreads(1)
	session, err := ort.NewDynamicAdvancedSession(path, []string{inputName}, []string{outputName}, opts)
	if err != nil {
		e.loadErr = fmt.Errorf("%w: session: %v", inference.ErrModelUnavailable, err)
		return e.loadErr
	}
	e.session = session
	return nil
}

// Estimate выполняет ViTPose+ на кропе бокса box=[x0,y0,x1,y1] (пиксели изображения img,
// уже уменьшенного до MaxSide) и возвращает 17 ключевых точек в тех же координатах.
func (e *Estimator) Estimate(ctx context.Context, img image.Image, box [4]float64) ([]horseid.KeyPoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if box[2] <= box[0] || box[3] <= box[1] {
		return nil, fmt.Errorf("%w: некорректный бокс %v", inference.ErrInvalidImage, box)
	}
	if err := e.ensureSession(); err != nil {
		return nil, err
	}
	in, err := ort.NewTensor(ort.NewShape(1, 3, InputHeight, InputWidth), preprocess(img, box))
	if err != nil {
		return nil, fmt.Errorf("vitpose: input tensor: %w", err)
	}
	defer in.Destroy()
	out, err := ort.NewEmptyTensor[float32](ort.NewShape(1, NumKeypoints, HeatmapHeight, HeatmapWidth))
	if err != nil {
		return nil, fmt.Errorf("vitpose: output tensor: %w", err)
	}
	defer out.Destroy()
	if err := e.session.Run([]ort.Value{in}, []ort.Value{out}); err != nil {
		return nil, fmt.Errorf("vitpose: run: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return decode(out.GetData(), box), nil
}

// Close освобождает сессию. Повторные вызовы безопасны.
func (e *Estimator) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	var err error
	if e.session != nil {
		err = e.session.Destroy()
		e.session = nil
	}
	e.loaded = true
	e.loadErr = inference.ErrModelUnavailable
	return err
}

// ONNX Runtime — процесс-глобальный, включая несколько оценщиков в одном процессе.
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

//go:build cgo

// cushki_onnx.go — ONNX-энкодер CUshki v6 (файнтюн DINOv2-L, экспорт
// export_dinov2_ft.py): вход «pixel_values» (1,3,448,448), выход «embedding»
// (динамическая размерность — читается из фактического выхода модели).
// Веса загружаются БЕЗ sha-верификации: путь задаётся конфигом ноды
// (inference.reid_model_path), файл внешний (fp32-вариант тянет sidecar
// .onnx.data) — проверка хеша здесь невозможна, целостность обеспечивается
// пиннедом sha256 в infra/scripts/download-cushki-model.sh.
package reid

import (
	"context"
	"fmt"
	"image"
	"math"
	"path/filepath"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	ort "github.com/yalue/onnxruntime_go"
)

// CushkiEncoder — ансамбль cushki-моделей (один или несколько членов).
type CushkiEncoder struct {
	sessions  []*ort.DynamicAdvancedSession
	inputSize int
}

// cushkiSession открывает ONNX с внешними тензорами: папка sidecar-файлов
// (.onnx.data) — каталог самого файла модели.
func cushkiSession(path string) (*ort.DynamicAdvancedSession, error) {
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer opts.Destroy()
	if err = opts.SetIntraOpNumThreads(4); err != nil {
		return nil, err
	}
	if err = opts.SetInterOpNumThreads(1); err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if err := opts.AddSessionConfigEntry("session.model_external_initializers_file_folder_path", dir); err != nil {
		return nil, err
	}
	return ort.NewDynamicAdvancedSession(path, []string{"pixel_values"}, []string{"embedding"}, opts)
}

// OpenCushkiEncoder открывает один или несколько членов ансамбля (пути в
// порядке блоков feature_layout). Первый член — основа (по нему считается
// ось ракурса в оригинале). Размерность выхода читается динамически при
// первом запуске, никакая константа не зашита.
func OpenCushkiEncoder(paths []string, inputSize int) (*CushkiEncoder, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("reid: cushki: no model paths")
	}
	if inputSize <= 0 {
		inputSize = 448
	}
	e := &CushkiEncoder{inputSize: inputSize}
	for _, path := range paths {
		sess, err := cushkiSession(path)
		if err != nil {
			e.Close()
			return nil, fmt.Errorf("reid: cushki: %s: %w", path, err)
		}
		e.sessions = append(e.sessions, sess)
	}
	return e, nil
}

func (e *CushkiEncoder) Close() error {
	var err error
	for _, s := range e.sessions {
		if cerr := s.Destroy(); cerr != nil && err == nil {
			err = cerr
		}
	}
	e.sessions = nil
	return err
}

// runDynamic запускает член ансамбля на кропе; размерность выхода берётся
// из фактически аллоцированного ORT тензора (nil-выход => авт аллокация).
func (e *CushkiEncoder) runDynamic(ctx context.Context, sess *ort.DynamicAdvancedSession, img *image.RGBA, box image.Rectangle) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	in, err := ort.NewTensor(ort.NewShape(1, 3, int64(e.inputSize), int64(e.inputSize)), tensorPixels(img, box, e.inputSize, true))
	if err != nil {
		return nil, err
	}
	defer in.Destroy()
	out := []ort.Value{nil}
	if err = sess.Run([]ort.Value{in}, out); err != nil {
		return nil, err
	}
	defer out[0].Destroy()
	tensor, ok := out[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("reid: cushki: unexpected output type")
	}
	shape := tensor.GetShape()
	if len(shape) != 2 || shape[0] != 1 || shape[1] <= 0 {
		return nil, fmt.Errorf("reid: cushki: unexpected output shape %v", shape)
	}
	return append([]float32(nil), tensor.GetData()...), ctx.Err()
}

// memberEmbeddings кодирует кроп всеми членами ансамбля: [raw_i, masked_i]
// на члена, в порядке членов (порядок блоков feature_layout).
func (e *CushkiEncoder) memberEmbeddings(ctx context.Context, img *image.RGBA, maskedBox image.Rectangle) ([][]float64, error) {
	out := make([][]float64, 0, len(e.sessions))
	for _, sess := range e.sessions {
		raw, err := e.runDynamic(ctx, sess, img, img.Bounds())
		if err != nil {
			return nil, err
		}
		masked, err := e.runDynamic(ctx, sess, img, maskedBox)
		if err != nil {
			return nil, err
		}
		out = append(out, cushkiConcat(raw, masked))
	}
	return out, nil
}

// cushkiConcat — [raw, masked]/sqrt(2) одного члена.
func cushkiConcat(raw, masked []float32) []float64 {
	out := make([]float64, 0, len(raw)+len(masked))
	const scale = math.Sqrt2
	for _, x := range raw {
		out = append(out, float64(x)/scale)
	}
	for _, x := range masked {
		out = append(out, float64(x)/scale)
	}
	return out
}

// fuseMembers — конкатенация членов ансамбля /sqrt(M) (остальная часть
// сборки признака CUshki, см. CushkiConfig.Features).
func fuseMembers(parts [][]float64) []float64 {
	m := len(parts)
	n := 0
	if m > 0 {
		n = len(parts[0])
	}
	out := make([]float64, 0, m*n)
	scale := math.Sqrt(float64(m))
	for _, p := range parts {
		for _, x := range p {
			out = append(out, x/scale)
		}
	}
	return out
}

// Embed кодирует детект CUshki-ансамблем: сшитый признак [raw, masked] по
// всем членам. masked — фон, залитый серым по маске сегментера (тот же приём,
// что у megadescriptor-пути); без сегментера — центральный кроп 80% бокса
// (отдельная версия CushkiCentralVersion, чтобы fallback не смешивался с
// полным пространством).
func (e *CushkiEncoder) Embed(ctx context.Context, src image.Image, box image.Rectangle, seg *Segmenter) (inference.EmbeddingResult, error) {
	img, err := cropForParts(src, box)
	if err != nil {
		return inference.EmbeddingResult{}, err
	}
	maskedBox := img.Bounds()
	version := CushkiVersion
	switch {
	case seg != nil:
		region, err := seg.Region(ctx, src, box, false)
		if err != nil {
			return inference.EmbeddingResult{}, err
		}
		if maskBackground(region.img, region.box, region.samples) {
			maskedBox = region.box
		} else {
			version = CushkiCentralVersion
			maskedBox = centralRect(img.Bounds())
		}
	default:
		// Нет сегментера: fallback центрального кропа в отдельном пространстве.
		version = CushkiCentralVersion
		maskedBox = centralRect(img.Bounds())
	}
	parts, err := e.memberEmbeddings(ctx, img, maskedBox)
	if err != nil {
		return inference.EmbeddingResult{}, err
	}
	fused := fuseMembers(parts)
	// float32 round-trip: EmbeddingResult хранит []float32.
	vec := make([]float32, len(fused))
	for i, x := range fused {
		vec[i] = float32(x)
	}
	return visualEmbedding(vec, version)
}

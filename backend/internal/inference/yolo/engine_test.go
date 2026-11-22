package yolo

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

// pngBytes кодирует изображение в PNG.
func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// TestEngineUnavailableWithoutModel — без весов/библиотеки движок деградирует
// в ErrModelUnavailable, приложение продолжает работать (ADR-0012).
func TestEngineUnavailableWithoutModel(t *testing.T) {
	e := New(Config{ModelPath: "/nonexistent/yolov8n.onnx", RuntimeLibPath: "/nonexistent/libonnxruntime.so"})
	validPNG := pngBytes(t, image.NewRGBA(rect(0, 0, 10, 10)))
	_, err := e.ExtractTraits(context.Background(), inference.ImageInput{Reader: bytes.NewReader(validPNG)})
	require.ErrorIs(t, err, inference.ErrModelUnavailable)
	_, err = e.Embed(context.Background(), inference.ImageInput{Reader: bytes.NewReader(validPNG)})
	require.ErrorIs(t, err, inference.ErrModelUnavailable)
}

// TestEngineNoSource — ни Reader, ни BlobStore: ErrInvalidImage.
func TestEngineNoSource(t *testing.T) {
	e := New(Config{})
	_, err := e.ExtractTraits(context.Background(), inference.ImageInput{})
	require.ErrorIs(t, err, inference.ErrInvalidImage)
}

// TestHistogramDelegateOnCrops — дефолтная эвристика даёт канонические
// значения и детерминирована.
func TestHistogramDelegateOnCrops(t *testing.T) {
	body := image.NewRGBA(rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			body.Set(x, y, color.RGBA{R: 160, G: 120, B: 80, A: 255}) // коричневый → bay
		}
	}
	head := image.NewRGBA(rect(0, 0, 100, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 100; x++ {
			head.Set(x, y, color.RGBA{R: 240, G: 235, B: 225, A: 255}) // светлая морда
		}
	}
	d := NewHistogramDelegate()
	traits, conf, err := d.ExtractTraitsFromCrops(context.Background(), Crops{Body: body, Head: head, Box: rect(0, 0, 200, 300)})
	require.NoError(t, err)
	require.Equal(t, "bay", traits["coat"])
	require.Equal(t, "light", traits["muzzle"])
	require.InDelta(t, 0.3, conf, 1e-9)
	for _, k := range inference.CanonicalKeys {
		require.Contains(t, traits, k)
	}
	traits2, _, err := d.ExtractTraitsFromCrops(context.Background(), Crops{Body: body, Head: head, Box: rect(0, 0, 200, 300)})
	require.NoError(t, err)
	require.Equal(t, traits, traits2)
}

// TestPoolEmbeddingDeterministic — вектор детерминирован и длиной 64.
func TestPoolEmbeddingDeterministic(t *testing.T) {
	img := image.NewRGBA(rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 60, A: 255})
		}
	}
	v1 := poolEmbedding(img)
	v2 := poolEmbedding(img)
	require.Len(t, v1, 64)
	require.Equal(t, v1, v2)
}

// TestPipelineRealModel — прогон на реальной модели. Требует переменных
// окружения (веса и библиотека — вне git):
//
//	YOLO_MODEL_PATH   — путь к yolov8n.onnx
//	YOLO_RUNTIME_LIB  — путь к libonnxruntime.so
//	YOLO_TEST_IMAGE   — путь к фото лошади (опционально)
//
// Честность (ТЗ): YOLO обучен на COCO, НЕ на лошадях Пржевальского; он
// детектит локацию животного, трейты — эвристика-предзаполнение.
func TestPipelineRealModel(t *testing.T) {
	modelPath := os.Getenv("YOLO_MODEL_PATH")
	libPath := os.Getenv("YOLO_RUNTIME_LIB")
	if modelPath == "" || libPath == "" {
		t.Skip("YOLO_MODEL_PATH/YOLO_RUNTIME_LIB не заданы: прогон на реальной модели пропущен")
	}
	e := New(Config{ModelPath: modelPath, RuntimeLibPath: libPath})
	t.Cleanup(func() { e.Close() })

	// Синтетическое изображение: серый прямоугольник — не лошадь. Честный
	// тест: детекция отсутствует с корректной ошибкой ErrNoAnimalDetected.
	synthetic := image.NewRGBA(rect(0, 0, 640, 640))
	for y := 0; y < 640; y++ {
		for x := 0; x < 640; x++ {
			synthetic.Set(x, y, color.RGBA{R: 128, G: 128, B: 128, A: 255})
		}
	}
	_, err := e.ExtractTraits(context.Background(), inference.ImageInput{Reader: bytes.NewReader(pngBytes(t, synthetic))})
	require.ErrorIs(t, err, inference.ErrNoAnimalDetected)

	if imgPath := os.Getenv("YOLO_TEST_IMAGE"); imgPath != "" {
		data, err := os.ReadFile(imgPath)
		require.NoError(t, err)
		res, err := e.ExtractTraits(context.Background(), inference.ImageInput{Reader: bytes.NewReader(data)})
		if err != nil {
			if errors.Is(err, inference.ErrNoAnimalDetected) {
				t.Logf("лошадь не найдена на %s: %v", imgPath, err)
				return
			}
			t.Fatalf("unexpected error: %v", err)
		}
		expectedVersion := ModelVersion
		if filepath.Base(modelPath) == "yolo26x.onnx" {
			expectedVersion = "yolo26x-coco-v1"
		}
		require.Equal(t, expectedVersion, res.ModelVersion)
		require.Greater(t, res.Confidence, 0.0)
		require.LessOrEqual(t, res.Confidence, 1.0)
		for _, k := range inference.CanonicalKeys {
			require.Contains(t, res.Traits, k)
		}
		emb, err := e.Embed(context.Background(), inference.ImageInput{Reader: bytes.NewReader(data)})
		require.NoError(t, err)
		require.Equal(t, expectedVersion, emb.ModelVersion)
		require.Len(t, emb.Vector, 64)
		t.Logf("REAL RUN: model=%s conf=%.3f traits=%v", res.ModelVersion, res.Confidence, res.Traits)
	}
}

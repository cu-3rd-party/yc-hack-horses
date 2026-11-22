//go:build cgo

package yolo

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

// BenchmarkDetector measures one decoded photograph per iteration on the production CPU path.
func BenchmarkDetector(b *testing.B) {
	dir, model, runtime := os.Getenv("YOLO_BENCH_PHOTOS"), os.Getenv("YOLO_MODEL_PATH"), os.Getenv("YOLO_RUNTIME_LIB")
	if dir == "" || model == "" || runtime == "" {
		b.Skip("set YOLO_BENCH_PHOTOS, YOLO_MODEL_PATH and YOLO_RUNTIME_LIB")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.jpg"))
	if err != nil || len(files) == 0 {
		b.Fatalf("no JPEG inputs in %q: %v", dir, err)
	}
	var photos []image.Image
	for _, name := range files {
		file, err := os.Open(name)
		if err != nil {
			b.Fatal(err)
		}
		img, err := decodeImage(context.Background(), nil, inference.ImageInput{Reader: file})
		file.Close()
		if err != nil {
			b.Fatal(err)
		}
		photos = append(photos, img)
	}
	e := New(Config{ModelPath: model, RuntimeLibPath: runtime})
	b.Cleanup(func() { e.Close() })
	if err := e.ensureSession(); err != nil {
		b.Fatal(err)
	}
	for i := range 3 {
		if _, _, err := e.detect(photos[i%len(photos)]); err != nil {
			b.Fatal(err)
		}
	}
	elapsed := make([]time.Duration, b.N)
	counts := make([]int, len(photos))
	b.ResetTimer()
	for i := range b.N {
		index := i % len(photos)
		start := time.Now()
		detections, _, err := e.detect(photos[index])
		elapsed[i] = time.Since(start)
		if err != nil {
			b.Fatal(err)
		}
		counts[index] = len(detections)
	}
	b.StopTimer()
	slices.Sort(elapsed)
	b.ReportMetric(float64(elapsed[(b.N-1)/2])/float64(time.Millisecond), "p50-ms")
	b.ReportMetric(float64(elapsed[(95*b.N+99)/100-1])/float64(time.Millisecond), "p95-ms")
	if b.N >= len(files) {
		for i, file := range files {
			b.Logf("%s %s: %d", e.version, filepath.Base(file), counts[i])
		}
	}
}

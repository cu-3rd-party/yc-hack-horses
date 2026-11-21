//go:build cgo

package yolo

import (
	"bytes"
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
)

// Optional local fixture: two JPEGs per named horse, with only altai-2 requiring assistance.
func TestVisualReIDOnFieldPhotos(t *testing.T) {
	dir, models := os.Getenv("REID_TEST_PHOTOS"), os.Getenv("INFER_REID_MODEL_DIR")
	if dir == "" || models == "" {
		t.Skip("set REID_TEST_PHOTOS and INFER_REID_MODEL_DIR to run the four-ID comparison")
	}
	e := New(Config{ModelPath: os.Getenv("YOLO_MODEL_PATH"), RuntimeLibPath: os.Getenv("YOLO_RUNTIME_LIB"), ReIDModelDir: models})
	defer e.Close()
	ctx := context.Background()
	type sample struct {
		embedding inference.EmbeddingResult
		assisted  bool
	}
	var gallery [2][]sample
	for visit, suffix := range []string{"-1.jpg", "-2.jpg"} {
		for _, id := range []string{"altai", "buran", "luna", "saran"} {
			data, err := os.ReadFile(filepath.Join(dir, id+suffix))
			require.NoError(t, err)
			input := func() inference.ImageInput { return inference.ImageInput{Reader: bytes.NewReader(data)} }
			v, err := e.Embed(ctx, input())
			s := sample{}
			if errors.Is(err, inference.ErrNoAnimalDetected) && id+suffix == "altai-2.jpg" {
				cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
				require.NoError(t, err)
				box := image.Rect(0, 0, cfg.Width, cfg.Height)
				rows, err := e.EmbedInstances(ctx, input(), &box)
				require.NoError(t, err)
				require.Len(t, rows, 1)
				v = rows[0].Embedding
				s.assisted = true
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, reid.Version, v.ModelVersion)
			require.Len(t, v.Vector, reid.Dimension)
			s.embedding = v
			gallery[visit] = append(gallery[visit], s)
		}
	}
	for _, allowBox := range []bool{false, true} {
		var hits [2]int
		var total int
		for visit, queries := range gallery {
			candidates := gallery[1-visit]
			for id, q := range queries {
				if !allowBox && (q.assisted || candidates[id].assisted) {
					continue
				}
				total++
				correct, err := reid.Similarity(q.embedding, candidates[id].embedding)
				require.NoError(t, err)
				rank := 1
				for j, c := range candidates {
					if j == id || (!allowBox && c.assisted) {
						continue
					}
					score, err := reid.Similarity(q.embedding, c.embedding)
					require.NoError(t, err)
					if score >= correct {
						rank++
					}
				}
				for k := range hits {
					if rank <= k+1 {
						hits[k]++
					}
				}
			}
		}
		t.Logf("provided_box=%t, MegaDescriptor top-1/top-2 out of %d: %v", allowBox, total, hits)
	}
	data, err := os.ReadFile(filepath.Join(dir, "pair.jpg"))
	require.NoError(t, err)
	rows, err := e.EmbedInstances(ctx, inference.ImageInput{Reader: bytes.NewReader(data)}, nil)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.NotEqual(t, rows[0].Box, rows[1].Box)
	for _, row := range rows {
		require.Empty(t, row.MeasurementError)
		require.NotNil(t, row.Traits)
		require.NotEmpty(t, row.Traits.Values)
		require.NoError(t, reid.Validate(row.Embedding))
	}
}

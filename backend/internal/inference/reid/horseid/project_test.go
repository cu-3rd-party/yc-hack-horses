package horseid

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// headsFixture — путь к настоящим весам из infra/reid-lab/export (в git не
// копируются); тесты с ними пропускаются, если файла нет.
func headsFixture(part string) string {
	return filepath.Join("..", "..", "..", "..", "..", "infra", "reid-lab", "export", "heads_dinov2_"+part+".f32")
}

// encodeProjection — little-endian float32: W построчно, затем b.
func encodeProjection(w, b []float32) []byte {
	raw := make([]byte, 4*(len(w)+len(b)))
	for i, v := range w {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(v))
	}
	for i, v := range b {
		binary.LittleEndian.PutUint32(raw[(len(w)+i)*4:], math.Float32bits(v))
	}
	return raw
}

// writeTemp — кладёт байты во временный файл и возвращает путь.
func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "heads.f32")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

// TestLoadProjectionEndianAndLayout — порядок байт little-endian и раскладка
// W[256][768] построчно + b[256] (иначе строки и смещения перепутаются).
func TestLoadProjectionEndianAndLayout(t *testing.T) {
	w := make([]float32, EmbedDim*DinoDim)
	b := make([]float32, EmbedDim)
	// W[i][j] = i + j/1024 — различимо по строке и столбцу.
	for i := 0; i < EmbedDim; i++ {
		for j := 0; j < DinoDim; j++ {
			w[i*DinoDim+j] = float32(i) + float32(j)/1024
		}
		b[i] = -float32(i) / 2
	}
	p, err := LoadProjection(writeTemp(t, encodeProjection(w, b)))
	require.NoError(t, err)
	require.Len(t, p.w, EmbedDim*DinoDim)
	require.Len(t, p.b, EmbedDim)
	require.Equal(t, float32(0), p.w[0])
	require.Equal(t, float32(EmbedDim-1)+float32(DinoDim-1)/1024, p.w[EmbedDim*DinoDim-1])
	require.Equal(t, -0.5*float32(EmbedDim-1), p.b[EmbedDim-1])
}

// TestLoadProjectionRejectsBadFiles — битый размер и нечисловые веса.
func TestLoadProjectionRejectsBadFiles(t *testing.T) {
	_, err := LoadProjection(filepath.Join(t.TempDir(), "absent.f32"))
	require.Error(t, err)

	_, err = LoadProjection(writeTemp(t, make([]byte, projectionBytes-4)))
	require.Error(t, err)

	w := make([]float32, EmbedDim*DinoDim)
	b := make([]float32, EmbedDim)
	w[17] = float32(math.NaN())
	_, err = LoadProjection(writeTemp(t, encodeProjection(w, b)))
	require.Error(t, err)
}

// TestApplyKnownMatrix — проекция по диагональной матрице: e = normalize(2x + b),
// проверяется и раскладка W, и нормировка результата.
func TestApplyKnownMatrix(t *testing.T) {
	w := make([]float32, EmbedDim*DinoDim)
	b := make([]float32, EmbedDim)
	for i := 0; i < EmbedDim; i++ {
		w[i*DinoDim+i] = 2
		b[i] = float32(i) / 100
	}
	p, err := LoadProjection(writeTemp(t, encodeProjection(w, b)))
	require.NoError(t, err)

	x := make([]float32, DinoDim)
	for i := range x {
		x[i] = float32(i%7) / 3
	}
	e, err := p.Apply(x)
	require.NoError(t, err)
	require.Len(t, e, EmbedDim)

	raw := make([]float64, EmbedDim)
	var sum float64
	for i := range raw {
		raw[i] = 2*float64(x[i]) + float64(b[i])
		sum += raw[i] * raw[i]
	}
	norm := math.Sqrt(sum)
	for i := range e {
		require.InDelta(t, raw[i]/norm, e[i], 1e-12)
	}

	_, err = p.Apply(make([]float32, DinoDim-1))
	require.Error(t, err)
}

// TestLoadRealHeads — настоящие веса из export/: размер файла, проекция
// нормированного вектора. Эталонные числа посчитаны на тех же весах
// в float64 (export_heads.py: W[256][768] + b[256]).
func TestLoadRealHeads(t *testing.T) {
	path := headsFixture("head")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("нет фикстуры %s", path)
	}
	p, err := LoadProjection(path)
	require.NoError(t, err)

	x := make([]float32, DinoDim)
	for j := range x {
		x[j] = float32(math.Sin(float64(j)*0.37) * 0.5)
	}
	e, err := p.Apply(x)
	require.NoError(t, err)
	require.Len(t, e, EmbedDim)

	var sum float64
	for _, v := range e {
		sum += v * v
	}
	require.InDelta(t, 1.0, math.Sqrt(sum), 1e-12)
	require.InDelta(t, 0.068772836, e[0], 1e-8)
	require.InDelta(t, -0.141476384, e[1], 1e-8)
	require.InDelta(t, -0.078307301, e[EmbedDim-1], 1e-8)

	body, err := LoadProjection(headsFixture("body"))
	require.NoError(t, err)
	_, err = body.Apply(x)
	require.NoError(t, err)
}

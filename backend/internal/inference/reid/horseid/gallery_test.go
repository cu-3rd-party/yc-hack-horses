package horseid

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeF32Rows пишет матрицу [rows][EmbedDim] little-endian float32.
// Значение NaN попадает в файл как есть (строка = отсутствие части).
func writeF32Rows(t *testing.T, path string, rows [][]float64) {
	t.Helper()
	buf := make([]byte, 0, len(rows)*EmbedDim*4)
	var four [4]byte
	for _, row := range rows {
		require.Len(t, row, EmbedDim)
		for _, v := range row {
			binary.LittleEndian.PutUint32(four[:], math.Float32bits(float32(v)))
			buf = append(buf, four[:]...)
		}
	}
	require.NoError(t, os.WriteFile(path, buf, 0o600))
}

// vec256 строит вектор длины EmbedDim с заданными ведущими компонентами.
func vec256(vals ...float64) []float64 {
	v := make([]float64, EmbedDim)
	copy(v, vals)
	return v
}

func nanVec256() []float64 {
	v := make([]float64, EmbedDim)
	for i := range v {
		v[i] = math.NaN()
	}
	return v
}

// TestLoadGallerySynthetic проверяет загрузку мини-галереи: NaN-строки -> отсутствие
// части, разбор метаданных, конфиг из meta.json и восстановление списка кличек.
func TestLoadGallerySynthetic(t *testing.T) {
	dir := t.TempDir()

	records := []galleryRecord{
		{PhotoID: "10", Horse: "Амага", Date: "2025-01-02", View: "left"},
		{PhotoID: "11", Horse: "Амага", Date: "2025-01-03", View: "front"},
		{PhotoID: "12", Horse: "Буда", Date: "2025-02-04", View: "rear"},
	}
	b, err := json.Marshal(records)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gallery.json"), b, 0o600))

	head := [][]float64{
		vec256(1, 0, 0),
		nanVec256(), // головы нет
		vec256(0, 1, 0),
	}
	body := [][]float64{
		vec256(0, 0, 1),
		vec256(0.5, 0, 0),
		vec256(0, 0, 0.25),
	}
	writeF32Rows(t, filepath.Join(dir, "gallery_dinov2_head.f32"), head)
	writeF32Rows(t, filepath.Join(dir, "gallery_dinov2_body.f32"), body)

	meta := `{"config":{"bb":"dinov2","w":0.5,"agg":"top2"},"horses":["Амага","Буда"],"dim":256,"temperature":20.0}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o600))

	g, err := LoadGallery(dir)
	require.NoError(t, err)

	require.Equal(t, []string{"Амага", "Буда"}, g.Horses)
	require.Len(t, g.Photos, 3)
	require.InDelta(t, 0.5, g.Config.WeightHead, 1e-12)
	require.Equal(t, "top2", g.Config.Agg)
	require.InDelta(t, 20.0, g.Config.Temp, 1e-12)

	require.NotNil(t, g.Photos[0].Head)
	require.Nil(t, g.Photos[1].Head, "NaN-строка должна давать отсутствие части")
	require.NotNil(t, g.Photos[1].Body, "NaN только в head не должен гасить body")
	require.InDelta(t, 1.0, g.Photos[0].Head[0], 1e-6)
	require.InDelta(t, 0.5, g.Photos[1].Body[0], 1e-6)
	require.Equal(t, ViewLeft, g.Photos[0].View)
	require.Equal(t, "Буда", g.Photos[2].Horse)
}

// TestLoadGalleryErrors проверяет отказ на несогласованных размерах и битом JSON.
func TestLoadGalleryErrors(t *testing.T) {
	base := func(t *testing.T) string {
		dir := t.TempDir()
		b, _ := json.Marshal([]galleryRecord{{PhotoID: "0", Horse: "А"}})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "gallery.json"), b, 0o600))
		writeF32Rows(t, filepath.Join(dir, "gallery_dinov2_head.f32"), [][]float64{vec256(1)})
		writeF32Rows(t, filepath.Join(dir, "gallery_dinov2_body.f32"), [][]float64{vec256(1)})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"),
			[]byte(`{"config":{"w":0.5,"agg":"top2"},"horses":["А"],"dim":256,"temperature":20}`), 0o600))
		return dir
	}

	t.Run("несовпадение строк", func(t *testing.T) {
		dir := base(t)
		// дописываем лишнюю строку в body -> 2 строки против 1 в gallery.json
		writeF32Rows(t, filepath.Join(dir, "gallery_dinov2_body.f32"),
			[][]float64{vec256(1), vec256(1)})
		_, err := LoadGallery(dir)
		require.Error(t, err)
	})

	t.Run("неверная размерность meta", func(t *testing.T) {
		dir := base(t)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"),
			[]byte(`{"config":{"w":0.5,"agg":"top2"},"horses":["А"],"dim":512,"temperature":20}`), 0o600))
		_, err := LoadGallery(dir)
		require.Error(t, err)
	})

	t.Run("отсутствует файл", func(t *testing.T) {
		dir := base(t)
		require.NoError(t, os.Remove(filepath.Join(dir, "gallery_dinov2_head.f32")))
		_, err := LoadGallery(dir)
		require.Error(t, err)
	})
}

// TestLoadGalleryConfigFallback проверяет подстановку констант контракта,
// когда meta.json не содержит w/agg/temperature.
func TestLoadGalleryConfigFallback(t *testing.T) {
	dir := t.TempDir()
	b, _ := json.Marshal([]galleryRecord{{PhotoID: "0", Horse: "А"}})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gallery.json"), b, 0o600))
	writeF32Rows(t, filepath.Join(dir, "gallery_dinov2_head.f32"), [][]float64{vec256(1)})
	writeF32Rows(t, filepath.Join(dir, "gallery_dinov2_body.f32"), [][]float64{vec256(1)})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"dim":256}`), 0o600))

	g, err := LoadGallery(dir)
	require.NoError(t, err)
	require.InDelta(t, WeightHead, g.Config.WeightHead, 1e-12)
	require.Equal(t, AggTop2, g.Config.Agg)
	require.InDelta(t, Temp, g.Config.Temp, 1e-12)
}

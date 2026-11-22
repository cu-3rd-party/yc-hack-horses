package vitpose

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"testing"
)

type refManifest struct {
	W     int    `json:"w"`
	H     int    `json:"h"`
	Image string `json:"image"`
	Boxes []struct {
		COCO [4]float64 `json:"coco"`
		PV   string     `json:"pv"`
	} `json:"boxes"`
	Heatmaps []struct {
		Box    [4]float64 `json:"box"`
		HM     string     `json:"hm"`
		Preds  string     `json:"preds"`
		Scores string     `json:"scores"`
	} `json:"heatmaps"`
}

// readF32 читает плоский массив float32 (little-endian). Необязательный want задаёт
// ожидаемое число элементов: эталон другого размера — это порча данных, а не повод
// упасть позже на индексации got[i]/want[i].
func readF32(t *testing.T, path string, want ...int) []float32 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(raw)%4 != 0 {
		t.Fatalf("read %s: размер %d не кратен 4", path, len(raw))
	}
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	if len(want) > 0 && len(out) != want[0] {
		t.Fatalf("read %s: %d элементов, ожидалось %d", path, len(out), want[0])
	}
	return out
}

func readF64(t *testing.T, path string, want ...int) []float64 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(raw)%8 != 0 {
		t.Fatalf("read %s: размер %d не кратен 8", path, len(raw))
	}
	out := make([]float64, len(raw)/8)
	for i := range out {
		out[i] = math.Float64frombits(binary.LittleEndian.Uint64(raw[i*8:]))
	}
	if len(want) > 0 && len(out) != want[0] {
		t.Fatalf("read %s: %d элементов, ожидалось %d", path, len(out), want[0])
	}
	return out
}

// preprocessMaxDiff сверяет тензор с эталоном: максимум |Δ| и косинус. Расхождение
// по длине — t.Fatalf (эталон и порт должны выдавать один и тот же тензор).
func preprocessMaxDiff(t *testing.T, name string, got, want []float32) (maxd, cos float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: длина тензора %d, эталон %d", name, len(got), len(want))
	}
	var dot, na, nb float64
	for i := range got {
		d := math.Abs(float64(got[i] - want[i]))
		if d > maxd {
			maxd = d
		}
		dot += float64(got[i]) * float64(want[i])
		na += float64(got[i]) * float64(got[i])
		nb += float64(want[i]) * float64(want[i])
	}
	if na > 0 && nb > 0 {
		cos = dot / (math.Sqrt(na) * math.Sqrt(nb))
	}
	return maxd, cos
}

// TestValidateAgainstRealTransformers сверяет Go-порт с эталоном, снятым реальным
// VitPoseImageProcessor (transformers 5.24.0, scipy 1.18.1). Пропускается, если каталог
// не задан (CI без модели).
func TestValidateAgainstRealTransformers(t *testing.T) {
	dir := os.Getenv("VITPOSE_REF_DIR")
	if dir == "" {
		t.Skip("VITPOSE_REF_DIR not set")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Skipf("no manifest: %v", err)
	}
	var m refManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	imgBytes, err := os.ReadFile(filepath.Join(dir, m.Image))
	if err != nil {
		t.Fatal(err)
	}
	if len(imgBytes) < m.W*m.H*3 {
		t.Fatalf("%s: %d байт, ожидалось %d", m.Image, len(imgBytes), m.W*m.H*3)
	}
	img := image.NewRGBA(image.Rect(0, 0, m.W, m.H))
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			i := (y*m.W + x) * 3
			off := img.PixOffset(x, y)
			img.Pix[off], img.Pix[off+1], img.Pix[off+2], img.Pix[off+3] = imgBytes[i], imgBytes[i+1], imgBytes[i+2], 255
		}
	}
	for _, b := range m.Boxes {
		coco := b.COCO
		box := [4]float64{coco[0], coco[1], coco[0] + coco[2], coco[1] + coco[3]}
		got := preprocess(img, box)
		want := readF32(t, filepath.Join(dir, b.PV), 3*InputHeight*InputWidth)
		maxd, cos := preprocessMaxDiff(t, fmt.Sprintf("preprocess coco=%v", coco), got, want)
		t.Logf("preprocess coco=%v: maxdiff=%.3e cos=%.9f", coco, maxd, cos)
		// Порог 1e-6 — допуск на ULP float32 (фактически ≤ 2.4e-7), а не на «похожесть».
		if maxd > 1e-6 {
			t.Errorf("preprocess coco=%v: maxdiff=%.3e больше 1e-6", coco, maxd)
		}
		if cos < 0.9999999 {
			t.Errorf("preprocess coco=%v cos=%.9f", coco, cos)
		}
	}
	for _, h := range m.Heatmaps {
		hm := readF32(t, filepath.Join(dir, h.HM), NumKeypoints*HeatmapHeight*HeatmapWidth)
		kps, err := decode(hm, [4]float64{h.Box[0], h.Box[1], h.Box[0] + h.Box[2], h.Box[1] + h.Box[3]})
		if err != nil {
			t.Fatalf("decode box=%v: %v", h.Box, err)
		}
		wantP := readF64(t, filepath.Join(dir, h.Preds), 2*NumKeypoints)
		wantS := readF64(t, filepath.Join(dir, h.Scores), NumKeypoints)
		var maxXY, maxS float64
		for i, kp := range kps {
			maxXY = math.Max(maxXY, math.Abs(kp.X-wantP[i*2]))
			maxXY = math.Max(maxXY, math.Abs(kp.Y-wantP[i*2+1]))
			maxS = math.Max(maxS, math.Abs(kp.Score-wantS[i]))
		}
		t.Logf("decode box=%v: max|dXY|=%.3e max|dScore|=%.3e", h.Box, maxXY, maxS)
		if maxXY > 1e-3 || maxS != 0 {
			t.Errorf("decode box=%v maxXY=%.3e maxS=%.3e", h.Box, maxXY, maxS)
		}
	}
}

// TestPreprocessOutOfBoundsBox фиксирует поведение на боксе, выходящем за границы фото
// (300x200, бокс 10,5,20,200 -> xyxy 10,5,30,205): кроп уходит ниже кадра, и там, где
// координата источника вне [0, side-1], должен стоять нулевой пиксель (после нормализации —
// -mean/std), как в scipy mode='constant' (интерполяции за краем массива нет).
// Эталон — реальный VitPoseImageProcessor, см. TestValidateAgainstRealTransformers.
func TestPreprocessOutOfBoundsBox(t *testing.T) {
	const w, h = 300, 200
	img := synthImage(w, h)
	got := preprocess(img, [4]float64{10, 5, 30, 205})
	if len(got) != 3*InputHeight*InputWidth {
		t.Fatalf("preprocess size %d", len(got))
	}
	for i, v := range got {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("пиксель %d не конечен: %v", i, v)
		}
	}
	// Последняя строка кропа целиком ниже фото: src_y ≈ 230 при h-1 = 199.
	fill := [3]float32{}
	for c := 0; c < 3; c++ {
		fill[c] = float32((0 - float64(imagenetMean[c])) / float64(imagenetStd[c]))
	}
	for x := 0; x < InputWidth; x++ {
		for c := 0; c < 3; c++ {
			i := (c*InputHeight+InputHeight-1)*InputWidth + x
			if math.Abs(float64(got[i]-fill[c])) > 1e-6 {
				t.Fatalf("c=%d x=%d: пиксель вне фото = %.7f, ожидался нулевой отсчёт %.7f",
					c, x, got[i], fill[c])
			}
		}
	}
	// Левый край того же кропа тоже вне фото (src_x < 0 при x_out=0).
	i := (0*InputHeight+InputHeight/2)*InputWidth + 0
	if math.Abs(float64(got[i]-fill[0])) > 1e-6 {
		t.Fatalf("левый край вне фото: %.7f, ожидался %.7f", got[i], fill[0])
	}
}

// TestValidateModelHeatmaps сверяет декод с реальными тепмапами модели ViTPose+ (веса
// usyd-community/vitpose-plus-base, dataset_index=3). Пропускается без каталога.
func TestValidateModelHeatmaps(t *testing.T) {
	dir := os.Getenv("VITPOSE_REF_DIR")
	if dir == "" {
		t.Skip("VITPOSE_REF_DIR not set")
	}
	boxes := [][4]float64{{60, 40, 210, 160}, {0, 0, 80, 60}, {250, 150, 290, 190}}
	for i, box := range boxes {
		hmPath := filepath.Join(dir, fmt.Sprintf("model_hm_%d.f32", i))
		if _, err := os.Stat(hmPath); err != nil {
			t.Skipf("no model heatmaps: %v", err)
		}
		hm := readF32(t, hmPath, NumKeypoints*HeatmapHeight*HeatmapWidth)
		kps, err := decode(hm, box)
		if err != nil {
			t.Fatalf("model decode box=%v: %v", box, err)
		}
		wantP := readF64(t, filepath.Join(dir, fmt.Sprintf("model_preds_%d.f64", i)), 2*NumKeypoints)
		wantS := readF64(t, filepath.Join(dir, fmt.Sprintf("model_scores_%d.f64", i)), NumKeypoints)
		var maxXY, maxS float64
		for k, kp := range kps {
			maxXY = math.Max(maxXY, math.Abs(kp.X-wantP[k*2]))
			maxXY = math.Max(maxXY, math.Abs(kp.Y-wantP[k*2+1]))
			maxS = math.Max(maxS, math.Abs(kp.Score-wantS[k]))
		}
		t.Logf("model decode box=%v: max|dXY|=%.3e max|dScore|=%.3e", box, maxXY, maxS)
		if maxXY > 1e-3 || maxS != 0 {
			t.Errorf("model decode box=%v maxXY=%.3e maxS=%.3e", box, maxXY, maxS)
		}
	}
}

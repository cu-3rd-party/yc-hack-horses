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

func readF32(t *testing.T, path string) []float32 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return out
}

func readF64(t *testing.T, path string) []float64 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out := make([]float64, len(raw)/8)
	for i := range out {
		out[i] = math.Float64frombits(binary.LittleEndian.Uint64(raw[i*8:]))
	}
	return out
}

// TestValidateAgainstRealTransformers сверяет Go-порт с эталоном, снятым реальным
// VitPoseImageProcessor (transformers). Пропускается, если каталог не задан (CI без модели).
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
		want := readF32(t, filepath.Join(dir, b.PV))
		var maxd, dot, na, nb float64
		for i := range got {
			d := math.Abs(float64(got[i] - want[i]))
			if d > maxd {
				maxd = d
			}
			dot += float64(got[i]) * float64(want[i])
			na += float64(got[i]) * float64(got[i])
			nb += float64(want[i]) * float64(want[i])
		}
		cos := dot / (math.Sqrt(na) * math.Sqrt(nb))
		t.Logf("preprocess coco=%v: maxdiff=%.3e cos=%.9f", coco, maxd, cos)
		if cos < 0.9999999 {
			t.Errorf("preprocess coco=%v cos=%.9f", coco, cos)
		}
	}
	for _, h := range m.Heatmaps {
		hm := readF32(t, filepath.Join(dir, h.HM))
		kps := decode(hm, [4]float64{h.Box[0], h.Box[1], h.Box[0] + h.Box[2], h.Box[1] + h.Box[3]})
		wantP := readF64(t, filepath.Join(dir, h.Preds))
		wantS := readF64(t, filepath.Join(dir, h.Scores))
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
		hm := readF32(t, hmPath)
		kps := decode(hm, box)
		wantP := readF64(t, filepath.Join(dir, fmt.Sprintf("model_preds_%d.f64", i)))
		wantS := readF64(t, filepath.Join(dir, fmt.Sprintf("model_scores_%d.f64", i)))
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

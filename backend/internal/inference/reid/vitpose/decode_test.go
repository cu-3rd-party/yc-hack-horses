package vitpose

import (
	"math"
	"testing"
)

func TestTransformPreds(t *testing.T) {
	center, scale := boxToCenterAndScale(60, 40, 150, 120)
	// углы тепмапы 48x64 -> края области padded (41.25..228.75, -25..225)
	x, y := transformPreds(0, 0, center, scale, HeatmapWidth, HeatmapHeight)
	almost(t, "tl.x", x, 41.25, 1e-3)
	almost(t, "tl.y", y, -25, 1e-3)
	x, y = transformPreds(HeatmapWidth-1, HeatmapHeight-1, center, scale, HeatmapWidth, HeatmapHeight)
	almost(t, "br.x", x, 228.75, 1e-3)
	almost(t, "br.y", y, 225, 1e-3)
}

// TestDecodeSymmetricPeak: симметричный пик -> argmax совпадает с вершиной, а DARK почти
// не смещает точку, поэтому декод равен переносу вершины в координаты фото.
func TestDecodeSymmetricPeak(t *testing.T) {
	plane := HeatmapHeight * HeatmapWidth
	heat := make([]float32, NumKeypoints*plane)
	const px, py = 24, 32
	for kp := 0; kp < NumKeypoints; kp++ {
		for y := 0; y < HeatmapHeight; y++ {
			for x := 0; x < HeatmapWidth; x++ {
				d2 := (x-px)*(x-px) + (y-py)*(y-py)
				heat[kp*plane+y*HeatmapWidth+x] = float32(1.0 / (1.0 + float64(d2)))
			}
		}
	}
	box := [4]float64{60, 40, 210, 160}
	kps, err := decode(heat, box)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	center, scale := boxToCenterAndScale(60, 40, 150, 120)
	wx, wy := transformPreds(px, py, center, scale, HeatmapWidth, HeatmapHeight)
	for kp := 0; kp < NumKeypoints; kp++ {
		almost(t, "peak.x", kps[kp].X, wx, 0.05)
		almost(t, "peak.y", kps[kp].Y, wy, 0.05)
		almost(t, "peak.score", kps[kp].Score, 1.0, 1e-6)
	}
}

// TestDecodeBadLength: тепмапы неверной длины -> ошибка (раньше был бы panic).
func TestDecodeBadLength(t *testing.T) {
	for _, n := range []int{0, NumKeypoints*HeatmapHeight*HeatmapWidth - 1, NumKeypoints*HeatmapHeight*HeatmapWidth + 1} {
		if _, err := decode(make([]float32, n), [4]float64{0, 0, 10, 10}); err == nil {
			t.Errorf("decode(len=%d): ожидалась ошибка", n)
		}
	}
}

// TestDecodeGolden сверяет декод (argmax + DARK + обратный аффинный перенос) с эталоном,
// снятым реальным post_process_pose_estimation (transformers, kernel_size=11).
func TestDecodeGolden(t *testing.T) {
	plane := HeatmapHeight * HeatmapWidth
	heat := make([]float32, NumKeypoints*plane)
	for kp := 0; kp < NumKeypoints; kp++ {
		px := 4 + (5*kp)%40
		py := 5 + (3*kp)%58
		for y := 0; y < HeatmapHeight; y++ {
			for x := 0; x < HeatmapWidth; x++ {
				d2 := (x-px)*(x-px) + (y-py)*(y-py)
				v := 1.0/(1.0+float64(d2)) + 0.01*float64(x)/48.0 + 0.006*float64(y)/64.0
				heat[kp*plane+y*HeatmapWidth+x] = float32(v)
			}
		}
	}
	kps, err := decode(heat, [4]float64{60, 40, 210, 160})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	golden := []struct{ x, y, score float64 }{
		{57.21104431152344, -5.156951904296875, 1.0013021230697632},
		{77.15785217285156, 6.747802734375, 1.0026249885559082},
		{97.10466003417969, 18.652572631835938, 1.0039479732513428},
		{117.05148315429688, 30.557342529296875, 1.0052708387374878},
		{136.998291015625, 42.46209716796875, 1.0065937042236328},
		{156.94509887695312, 54.36686706542969, 1.0079166889190674},
		{176.89190673828125, 66.27162170410156, 1.0092395544052124},
		{196.83868408203125, 78.1763916015625, 1.010562539100647},
		{57.21104431152344, 90.08114624023438, 1.0035520792007446},
		{77.15785217285156, 101.98590087890625, 1.0048749446868896},
		{97.10466003417969, 113.89067077636719, 1.0061979293823242},
		{117.05148315429688, 125.79542541503906, 1.0075207948684692},
		{136.998291015625, 137.7001953125, 1.0088437795639038},
		{156.94508361816406, 149.60494995117188, 1.0101666450500488},
		{176.89190673828125, 161.50970458984375, 1.0114896297454834},
		{196.83868408203125, 173.41448974609375, 1.0128124952316284},
		{57.21104431152344, 185.31924438476562, 1.005802035331726},
	}
	for kp, g := range golden {
		if math.Abs(kps[kp].X-g.x) > 1e-3 || math.Abs(kps[kp].Y-g.y) > 1e-3 {
			t.Errorf("kp%d: got (%.6f,%.6f) want (%.6f,%.6f)", kp, kps[kp].X, kps[kp].Y, g.x, g.y)
		}
		if kps[kp].Score != g.score {
			t.Errorf("kp%d score: got %v want %v", kp, kps[kp].Score, g.score)
		}
	}
}

package vitpose

import (
	"math"
	"testing"
)

func almost(t *testing.T, name string, got, want, eps float64) {
	t.Helper()
	if math.Abs(got-want) > eps {
		t.Errorf("%s: got %.12g want %.12g (eps %.1e)", name, got, want, eps)
	}
}

// TestBoxToCenterAndScale сверяет box_to_center_and_scale с эталоном transformers
// (значения сняты реальным VitPoseImageProcessor).
func TestBoxToCenterAndScale(t *testing.T) {
	cases := []struct {
		x, y, w, h     float64
		cx, cy, sx, sy float32
	}{
		{60, 40, 150, 120, 135, 100, 0.9375, 1.25},
		{0, 0, 80, 60, 40, 30, 0.5, 0.6666667},
		{120, 90, 300, 200, 270, 190, 1.875, 2.5},
		{250, 150, 40, 40, 270, 170, 0.25, 0.33333334},
	}
	for _, c := range cases {
		center, scale := boxToCenterAndScale(c.x, c.y, c.w, c.h)
		almost(t, "center.x", float64(center[0]), float64(c.cx), 1e-5)
		almost(t, "center.y", float64(center[1]), float64(c.cy), 1e-5)
		almost(t, "scale.w", float64(scale[0]), float64(c.sx), 1e-6)
		almost(t, "scale.h", float64(scale[1]), float64(c.sy), 1e-6)
	}
}

// TestWarpPullMatrix сверяет инверсную (pull) матрицу scipy_warp_affine с эталоном.
func TestWarpPullMatrix(t *testing.T) {
	// box (60,40,150,120): линейная часть диагональна, сдвиги из эталона.
	lin, off := warpPullMatrix([4]float64{60, 40, 210, 160})
	almost(t, "lin00", lin[0][0], 0.9803921751955851, 1e-9)
	almost(t, "lin01", lin[0][1], 0, 1e-12)
	almost(t, "lin10", lin[1][0], 0, 1e-12)
	almost(t, "lin11", lin[1][1], 0.981675432801709, 1e-9)
	almost(t, "off0", off[0], -25.00000046748742, 1e-6)
	almost(t, "off1", off[1], 41.25000213570316, 1e-6)

	lin, off = warpPullMatrix([4]float64{0, 0, 80, 60})
	almost(t, "lin00b", lin[0][0], 0.5228758430668372, 1e-9)
	almost(t, "lin11b", lin[1][1], 0.523560218573669, 1e-9)
	almost(t, "off0b", off[0], -36.66667248428806, 1e-6)
	almost(t, "off1b", off[1], -10.00000037447945, 1e-6)
}

// TestWarpPullMapping проверяет отображение углов кропа 192x256 в пиксели исходного фото.
// Кроп покрывает область padded = scale*200, центрированную на боксе; «unbiased» диапазон
// [0, side-1] отображает края кропа на края области.
func TestWarpPullMapping(t *testing.T) {
	lin, off := warpPullMatrix([4]float64{60, 40, 210, 160})
	src := func(xo, yo int) (float64, float64) {
		sx := lin[1][0]*float64(yo) + lin[1][1]*float64(xo) + off[1]
		sy := lin[0][0]*float64(yo) + lin[0][1]*float64(xo) + off[0]
		return sx, sy
	}
	// центр бокса (135,100); padded w=187.5, h=250 -> края 41.25..228.75 и -25..225.
	cases := []struct {
		xo, yo int
		wx, wy float64
	}{
		{0, 0, 41.25, -25},
		{191, 0, 228.75, -25},
		{0, 255, 41.25, 225},
		{191, 255, 228.75, 225},
	}
	for _, c := range cases {
		sx, sy := src(c.xo, c.yo)
		almost(t, "map x", sx, c.wx, 1e-4)
		almost(t, "map y", sy, c.wy, 1e-4)
	}
}

func TestInverse3x3(t *testing.T) {
	m := [3][3]float64{{2, 0, 3}, {0, 4, 5}, {0, 0, 1}}
	inv := inverse3x3(m)
	almost(t, "inv00", inv[0][0], 0.5, 1e-12)
	almost(t, "inv02", inv[0][2], -1.5, 1e-12)
	almost(t, "inv11", inv[1][1], 0.25, 1e-12)
	almost(t, "inv12", inv[1][2], -1.25, 1e-12)
}

package horseid

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// kp — короткий конструктор ключевой точки.
func kp(x, y, score float64) KeyPoint { return KeyPoint{X: x, Y: y, Score: score} }

// horse17 собирает 17 точек: заданные индексы, остальные — нули (score 0 < KpScoreMin).
func horse17(pts map[int]KeyPoint) []KeyPoint {
	k := make([]KeyPoint, 17)
	for i, p := range pts {
		k[i] = p
	}
	return k
}

func TestClipBox(t *testing.T) {
	cases := []struct {
		name string
		box  [4]float64
		w, h int
		want [4]float64
	}{
		{"внутри кадра", [4]float64{10, 20, 30, 40}, 100, 100, [4]float64{10, 20, 30, 40}},
		{"отрицательные координаты", [4]float64{-5, -8, 30, 40}, 100, 100, [4]float64{0, 0, 30, 40}},
		{"за кадром справа и снизу", [4]float64{10, 20, 130, 140}, 100, 100, [4]float64{10, 20, 100, 100}},
		// clip_box не нормализует: за кадром обрезается только правая/нижняя граница.
		{"бокс целиком правее и ниже кадра", [4]float64{120, 130, 150, 160}, 100, 100, [4]float64{120, 130, 100, 100}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClipBox(c.box, c.w, c.h); got != c.want {
				t.Fatalf("ClipBox(%v,%d,%d)=%v, want %v", c.box, c.w, c.h, got, c.want)
			}
		})
	}
}

func TestHeadBox(t *testing.T) {
	box := func(v [4]float64) *[4]float64 { return &v }
	cases := []struct {
		name string
		kps  []KeyPoint
		w, h int
		want *[4]float64
	}{
		{
			name: "нос+глаз+ухо, ровно порог 48 — голова есть",
			kps: horse17(map[int]KeyPoint{
				0: kp(100, 100, .9), 1: kp(110, 90, .8), 2: kp(90, 90, .8),
				3: kp(115, 80, .7), 4: kp(85, 80, .7),
			}),
			w: 200, h: 200,
			want: box([4]float64{76, 66, 124, 114}), // bbox 30x20 → side 48, центр (100,90)
		},
		{
			name: "сторона меньше 48 — головы нет",
			kps: horse17(map[int]KeyPoint{
				0: kp(100, 100, .9), 1: kp(110, 90, .8), 3: kp(114, 80, .7),
			}), // bbox 14x20 → side 32
			w: 200, h: 200,
			want: nil,
		},
		{
			name: "меньше 3 точек (нос+глаз)",
			kps: horse17(map[int]KeyPoint{
				0: kp(100, 100, .9), 1: kp(110, 90, .8),
			}),
			w: 200, h: 200,
			want: nil,
		},
		{
			name: "нет носа",
			kps: horse17(map[int]KeyPoint{
				1: kp(110, 90, .8), 2: kp(90, 90, .8), 3: kp(115, 80, .7),
			}),
			w: 200, h: 200,
			want: nil,
		},
		{
			name: "есть нос и 3 точки, но ни одного глаза",
			kps: horse17(map[int]KeyPoint{
				0: kp(100, 100, .9), 3: kp(115, 80, .7), 4: kp(85, 80, .7),
			}),
			w: 200, h: 200,
			want: nil,
		},
		{
			name: "нос на пороге 0.3 не считается",
			kps: horse17(map[int]KeyPoint{
				0: kp(100, 100, KpScoreMin), 1: kp(110, 90, .8), 3: kp(115, 80, .7),
			}),
			w: 200, h: 200,
			want: nil,
		},
		{
			name: "точка ниже порога не расширяет bbox",
			kps: horse17(map[int]KeyPoint{
				0: kp(100, 100, .9), 1: kp(140, 90, .8), 3: kp(100, 60, .7),
				4: kp(0, 0, .1), // далеко, но score < KpScoreMin — игнор
			}),
			w: 200, h: 200,
			want: box([4]float64{88, 48, 152, 112}), // bbox 40x40 → side 64, центр (120,80)
		},
		{
			name: "обрезка по кадру",
			kps: horse17(map[int]KeyPoint{
				0: kp(5, 5, .9), 1: kp(45, 5, .8), 3: kp(5, 45, .7),
			}), // bbox 40x40 → side 64, центр (25,25) → [-7,-7,57,57]
			w: 50, h: 50,
			want: box([4]float64{0, 0, 50, 50}),
		},
		{
			name: "меньше 5 точек на входе",
			kps:  []KeyPoint{kp(100, 100, .9), kp(110, 90, .8), kp(115, 80, .7)},
			w:    200, h: 200,
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := HeadBox(c.kps, c.w, c.h)
			if (got == nil) != (c.want == nil) {
				t.Fatalf("HeadBox=%v, want %v", got, c.want)
			}
			if got != nil && *got != *c.want {
				t.Fatalf("HeadBox=%v, want %v", *got, *c.want)
			}
		})
	}
}

func TestBodyBox(t *testing.T) {
	cases := []struct {
		name string
		body [4]float64
		w, h int
		want [4]float64
	}{
		{"запас по всем сторонам", [4]float64{100, 100, 200, 200}, 500, 500, [4]float64{92, 92, 208, 208}},
		{"обрезка слева и сверху", [4]float64{0, 0, 100, 100}, 50, 50, [4]float64{0, 0, 50, 50}},
		{"обрезка справа и снизу", [4]float64{40, 40, 60, 60}, 50, 50, [4]float64{32, 32, 50, 50}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BodyBox(c.body, c.w, c.h); got != c.want {
				t.Fatalf("BodyBox(%v,%d,%d)=%v, want %v", c.body, c.w, c.h, got, c.want)
			}
		})
	}
}

func TestComputeView(t *testing.T) {
	body := [4]float64{0, 0, 100, 100} // ширина 100, порог 0.25 → 25 px
	cases := []struct {
		name string
		kps  []KeyPoint
		want View
	}{
		{
			name: "нос сильно левее центра бёдер — left",
			kps:  horse17(map[int]KeyPoint{0: kp(10, 50, .9), 11: kp(60, 50, .9), 12: kp(70, 50, .9)}),
			want: ViewLeft,
		},
		{
			name: "нос правее порога — right",
			kps:  horse17(map[int]KeyPoint{0: kp(91, 50, .9), 11: kp(60, 50, .9), 12: kp(70, 50, .9)}),
			want: ViewRight,
		},
		{
			name: "ровно на пороге 0.25 — front",
			kps:  horse17(map[int]KeyPoint{0: kp(90, 50, .9), 11: kp(60, 50, .9), 12: kp(70, 50, .9)}),
			want: ViewFront,
		},
		{
			name: "нос внутри порога — front",
			kps:  horse17(map[int]KeyPoint{0: kp(50, 50, .9), 11: kp(60, 50, .9), 12: kp(70, 50, .9)}),
			want: ViewFront,
		},
		{
			name: "один бок по одному бедру",
			kps:  horse17(map[int]KeyPoint{0: kp(10, 50, .9), 11: kp(60, 50, .9), 12: kp(70, 50, .1)}),
			want: ViewLeft,
		},
		{
			name: "носа нет, есть бёдра — rear",
			kps:  horse17(map[int]KeyPoint{0: kp(50, 50, .1), 11: kp(60, 50, .9), 12: kp(70, 50, .9)}),
			want: ViewRear,
		},
		{
			name: "нет ни носа, ни бёдер — unknown",
			kps:  horse17(map[int]KeyPoint{0: kp(50, 50, .1), 11: kp(60, 50, .1), 12: kp(70, 50, .1)}),
			want: ViewUnknown,
		},
		{
			name: "есть нос, но нет бёдер — unknown",
			kps:  horse17(map[int]KeyPoint{0: kp(10, 50, .9), 11: kp(60, 50, .1), 12: kp(70, 50, .1)}),
			want: ViewUnknown,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ComputeView(c.kps, body); got != c.want {
				t.Fatalf("ComputeView=%q, want %q", got, c.want)
			}
		})
	}
}

// gradientImage — картинка, где R=x, G=y: по цвету пикселя проверяется смещение кропа.
func gradientImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0, A: 255})
		}
	}
	return img
}

func decodePNG(t *testing.T, raw []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	return img
}

func rgbAt(t *testing.T, img image.Image, x, y int) (uint8, uint8) {
	t.Helper()
	r, g, _, _ := img.At(x, y).RGBA()
	return uint8(r >> 8), uint8(g >> 8)
}

func TestCropPNG(t *testing.T) {
	img := gradientImage(100, 100)
	cases := []struct {
		name    string
		box     [4]float64
		wantW   int
		wantH   int
		originX int
		originY int
	}{
		{"точный бокс", [4]float64{10, 20, 40, 50}, 30, 30, 10, 20},
		{"усечение дробных координат", [4]float64{10.7, 20.9, 40.2, 50.8}, 30, 30, 10, 20},
		{"обрезка по кадру", [4]float64{-10, -10, 50, 50}, 50, 50, 0, 0},
		{"обрезка справа снизу", [4]float64{60, 60, 130, 130}, 40, 40, 60, 60},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := CropPNG(img, c.box)
			if err != nil {
				t.Fatalf("CropPNG: %v", err)
			}
			out := decodePNG(t, raw)
			if out.Bounds().Dx() != c.wantW || out.Bounds().Dy() != c.wantH {
				t.Fatalf("размер %dx%d, want %dx%d", out.Bounds().Dx(), out.Bounds().Dy(), c.wantW, c.wantH)
			}
			r, g := rgbAt(t, out, 0, 0)
			if int(r) != c.originX || int(g) != c.originY {
				t.Fatalf("пиксель (0,0)=(%d,%d), want (%d,%d)", r, g, c.originX, c.originY)
			}
			// правый нижний пиксель смещён ровно на размер кропа
			r, g = rgbAt(t, out, c.wantW-1, c.wantH-1)
			if int(r) != c.originX+c.wantW-1 || int(g) != c.originY+c.wantH-1 {
				t.Fatalf("пиксель (%d,%d)=(%d,%d)", c.wantW-1, c.wantH-1, r, g)
			}
		})
	}
}

func TestCropPNGEmpty(t *testing.T) {
	img := gradientImage(100, 100)
	cases := []struct {
		name string
		box  [4]float64
	}{
		{"перевёрнутый бокс", [4]float64{50, 50, 40, 40}},
		{"бокс целиком за кадром", [4]float64{200, 200, 300, 300}},
		{"нулевая ширина", [4]float64{10, 10, 10, 40}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := CropPNG(img, c.box); !errors.Is(err, ErrEmptyCrop) {
				t.Fatalf("err=%v, want ErrEmptyCrop", err)
			}
		})
	}
}

func TestBodyAndHeadCropPNG(t *testing.T) {
	img := gradientImage(100, 100)

	bodyRaw, err := BodyCropPNG(img, [4]float64{10, 10, 40, 40})
	if err != nil {
		t.Fatalf("BodyCropPNG: %v", err)
	}
	body := decodePNG(t, bodyRaw)
	if body.Bounds().Dx() != 46 || body.Bounds().Dy() != 46 { // [2,2,48,48]
		t.Fatalf("body %dx%d, want 46x46", body.Bounds().Dx(), body.Bounds().Dy())
	}
	if r, g := rgbAt(t, body, 0, 0); r != 2 || g != 2 {
		t.Fatalf("body origin=(%d,%d), want (2,2)", r, g)
	}

	headRaw, err := HeadCropPNG(img, [4]float64{10, 10, 50, 50})
	if err != nil {
		t.Fatalf("HeadCropPNG: %v", err)
	}
	head := decodePNG(t, headRaw)
	if head.Bounds().Dx() != 40 || head.Bounds().Dy() != 40 {
		t.Fatalf("head %dx%d, want 40x40", head.Bounds().Dx(), head.Bounds().Dy())
	}
}

//go:build cgo

package horseid

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

// pillowPattern — детерминированная картинка 13x7 из теста сверки с Pillow.
func pillowPattern() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 13, 7))
	for y := 0; y < 7; y++ {
		for x := 0; x < 13; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8((x*37 + y*11) % 256),
				G: uint8((x*17 + y*29) % 256),
				B: uint8((x*x + y*7) % 256),
				A: 255,
			})
		}
	}
	return img
}

// TestResampleRGBMatchesPillow — побайтовая сверка bicubic с Pillow
// (Image.resize((8,8), BICUBIC) для 13x7): 0 расхождений на всём кадре.
func TestResampleRGBMatchesPillow(t *testing.T) {
	got := resampleRGB(pillowPattern(), 8, 8)
	require.Len(t, got, 8*8*3)
	want := []uint8{
		12, 5, 0, 71, 32, 4, 132, 60, 13, 203, 87, 27,
		73, 115, 47, 46, 142, 71, 117, 170, 102, 176, 197, 136,
		21, 29, 6, 80, 56, 10, 141, 84, 19, 212, 111, 33,
		82, 139, 53, 55, 166, 77, 126, 193, 108, 185, 230, 142,
		31, 55, 12, 90, 82, 16, 151, 110, 25, 222, 137, 39,
		92, 165, 59, 65, 192, 83, 136, 244, 114, 195, 143, 148,
		40, 80, 18, 99, 107, 22, 159, 135, 31, 237, 162, 45,
		107, 189, 65, 73, 229, 89, 145, 130, 120, 204, 47, 154,
		50, 106, 24, 109, 133, 28, 174, 161, 37, 204, 187, 51,
		74, 231, 71, 88, 205, 95, 155, 18, 126, 214, 31, 160,
		59, 131, 30, 118, 158, 34, 190, 186, 43, 156, 224, 57,
		26, 205, 77, 104, 101, 101, 164, 27, 132, 223, 68, 166,
		69, 157, 36, 128, 184, 40, 199, 212, 49, 172, 215, 63,
		42, 64, 83, 113, 21, 107, 174, 67, 138, 233, 93, 172,
		78, 181, 42, 137, 208, 46, 208, 232, 55, 181, 28, 69,
		51, 29, 89, 122, 62, 113, 183, 90, 144, 242, 117, 178,
	}
	require.Equal(t, want, got)
}

// TestBicubicWeight — ключевые точки свёртки Keys (a=-0.5): w(0)=1, w(1)=0,
// w(2)=0, w(0.5)=9/16, w(1.5)=-1/16 (отрицательные лепестки — признак a=-0.5).
func TestBicubicWeight(t *testing.T) {
	require.InDelta(t, 1.0, bicubicWeight(0), 1e-12)
	require.InDelta(t, 0.0, bicubicWeight(1), 1e-12)
	require.InDelta(t, 0.0, bicubicWeight(2), 1e-12)
	require.InDelta(t, 0.5625, bicubicWeight(0.5), 1e-12)
	require.InDelta(t, -0.0625, bicubicWeight(1.5), 1e-12)
	// фильтр чётный: w(-t) = w(t)
	require.InDelta(t, -0.0625, bicubicWeight(-1.5), 1e-12)
}

// TestResampleKernelsPreserveConstant — нормировка весов: однотонная картинка
// после ресайза остаётся однотонной (сумма весов = 1, без краевых артефактов).
func TestResampleKernelsPreserveConstant(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 60))
	for y := 0; y < 60; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 200, B: 30, A: 255})
		}
	}
	got := resampleRGB(img, 224, 224)
	require.Len(t, got, 3*224*224)
	for i := 0; i < len(got); i += 3 {
		require.Equal(t, []uint8{10, 200, 30}, got[i:i+3], "пиксель %d", i/3)
	}
}

// TestDinoPixelsLayout — NCHW, порядок каналов RGB, ImageNet-нормировка.
func TestDinoPixelsLayout(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 300, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 300; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	pixels, err := DinoPixels(img)
	require.NoError(t, err)
	plane := DinoSize * DinoSize
	require.Len(t, pixels, 3*plane)

	require.InDelta(t, (1-0.485)/0.229, pixels[0], 1e-6)
	require.InDelta(t, (0-0.456)/0.224, pixels[plane], 1e-6)
	require.InDelta(t, (0-0.406)/0.225, pixels[2*plane], 1e-6)
	// один и тот же пиксель по всему кадру — одно и то же значение
	require.InDelta(t, pixels[0], pixels[plane-1], 1e-6)

	_, err = DinoPixels(nil)
	require.ErrorIs(t, err, inference.ErrInvalidImage)
	_, err = DinoPixels(image.NewRGBA(image.Rect(0, 0, 0, 0)))
	require.ErrorIs(t, err, inference.ErrInvalidImage)
}

// TestDinoPixelsMatchesPillow — сверка полного препроцессинга DINOv2 с Pillow:
// resize((224,224), BICUBIC) → ToTensor → Normalize(IMNET) для кадра 37x23
// (значения посчитаны Python-эталоном demo_app.py). Допуск — float32.
func TestDinoPixelsMatchesPillow(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 37, 23))
	for y := 0; y < 23; y++ {
		for x := 0; x < 37; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8((x*37 + y*11) % 256),
				G: uint8((x*17 + y*29) % 256),
				B: uint8((x*x + y*7) % 256),
				A: 255,
			})
		}
	}
	pixels, err := DinoPixels(img)
	require.NoError(t, err)
	plane := DinoSize * DinoSize
	checks := []struct {
		index int
		want  [3]float64
	}{
		{0, [3]float64{-2.11790393, -2.035714286, -1.804444444}},
		{100, [3]float64{-0.696549362, -2.00070028, -1.804444444}},
		{20000, [3]float64{1.649541913, 0.922969188, 1.053943355}},
		{50175, [3]float64{-1.3130405, 2.025910364, 1.315381264}},
	}
	for _, c := range checks {
		for ch := 0; ch < 3; ch++ {
			require.InDelta(t, c.want[ch], pixels[ch*plane+c.index], 1e-5,
				"пиксель %d, канал %d", c.index, ch)
		}
	}
}

// TestL2Normalize — единичная норма, нулевой вектор — ошибка.
func TestL2Normalize(t *testing.T) {
	f32 := []float32{3, 0, 4}
	require.NoError(t, l2Normalize(f32))
	require.InDelta(t, 0.6, f32[0], 1e-6)
	require.InDelta(t, 0.8, f32[2], 1e-6)

	f64 := []float64{1, math.Sqrt(3)}
	require.NoError(t, l2Normalize(f64))
	require.InDelta(t, 0.5, f64[0], 1e-12)

	require.Error(t, l2Normalize([]float64{0, 0}))
	require.Error(t, l2Normalize([]float64{math.NaN(), 1}))
}

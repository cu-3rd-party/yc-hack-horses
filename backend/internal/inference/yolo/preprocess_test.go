package yolo

import (
	"image"
	"image/color"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

// rect — короткая запись image.Rectangle.
func rect(x0, y0, x1, y1 int) image.Rectangle {
	return image.Rect(x0, y0, x1, y1)
}

// TestLetterboxTensorShapeAndScale — letterbox 640x640, NCHW-тензор,
// пропорции сохранены, паддинг серый.
func TestLetterboxTensorShapeAndScale(t *testing.T) {
	img := image.NewRGBA(rect(0, 0, 320, 640)) // вертикальная полоса
	for y := 0; y < 640; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	lr, err := letterboxTensor(img)
	require.NoError(t, err)
	// NCHW 1x3x640x640 → 3*640*640 значений
	require.Len(t, lr.data, 3*inputW*inputH)
	// scale = 640/640 = 1, но ширина 320 → newW = 320, padX = 160
	require.InDelta(t, 1.0, lr.params.scale, 1e-9)
	require.Equal(t, 160, lr.params.padX)
	require.Equal(t, 0, lr.params.padY)

	data := lr.data
	plane := inputW * inputH
	// центр картинки — исходный цвет
	// центр картинки: idx = y*640 + (padX + x) = 320*640 + (160+160)
	center := inputW*320 + 320
	require.InDelta(t, 200.0/255, float64(data[center]), 0.01)
	require.InDelta(t, 100.0/255, float64(data[plane+center]), 0.01)
	require.InDelta(t, 50.0/255, float64(data[2*plane+center]), 0.01)
	// паддинг слева — серый 114/255
	pad := inputW*320 + 10
	require.InDelta(t, 114.0/255, float64(data[pad]), 0.005)
}

// TestLetterboxUpscale — маленькое изображение увеличивается до 640.
func TestLetterboxUpscale(t *testing.T) {
	img := image.NewRGBA(rect(0, 0, 100, 100))
	lr, err := letterboxTensor(img)
	require.NoError(t, err)
	require.InDelta(t, 6.4, lr.params.scale, 1e-9)
	require.Equal(t, 0, lr.params.padX)
	require.Equal(t, 0, lr.params.padY)
}

// TestLetterboxEmpty — пустое изображение даёт ошибку invalid image.
func TestLetterboxEmpty(t *testing.T) {
	img := image.NewRGBA(rect(0, 0, 0, 0))
	_, err := letterboxTensor(img)
	require.ErrorIs(t, err, inference.ErrInvalidImage)
}

// TestMakeCrops — кропы корпуса и головы внутри границ изображения.
func TestMakeCrops(t *testing.T) {
	img := image.NewRGBA(rect(0, 0, 1000, 800))
	b := rect(100, 100, 900, 700)
	crops := makeCrops(img, b)
	require.Equal(t, b, crops.Box)
	// корпус: бокс + 5% запас с каждой стороны (800+40+40, 600+30+30)
	require.Equal(t, 880, crops.Body.Bounds().Dx())
	require.Equal(t, 660, crops.Body.Bounds().Dy())
	// голова: 30% высоты, 60% ширины бокса
	require.Equal(t, 480, crops.Head.Bounds().Dx())
	require.Equal(t, 180, crops.Head.Bounds().Dy())
}

// TestMakeCropsClamped — бокс частично вне изображения: кроп обрезается.
func TestMakeCropsClamped(t *testing.T) {
	img := image.NewRGBA(rect(0, 0, 100, 100))
	crops := makeCrops(img, rect(50, 50, 200, 200))
	require.NotNil(t, crops.Body)
	// бокс 50..200 + 5% (7) → 43..207, обрезка до 100 → ширина 57
	require.Equal(t, 57, crops.Body.Bounds().Dx())
}

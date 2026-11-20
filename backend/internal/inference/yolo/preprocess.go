package yolo

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

// letterboxParams — параметры letterbox для обратного маппинга координат.
type letterboxParams struct {
	scale   float64 // масштаб оригинала → вход
	padX    int     // отступ слева
	padY    int     // отступ сверху
	srcRect image.Rectangle
}

// letterboxResult — NCHW-данные входа и параметры letterbox.
// Тензор создаётся отдельно (после инициализации onnxruntime).
type letterboxResult struct {
	data   []float32
	params letterboxParams
}

// decodeImage читает байты (BlobStore или Reader) и декодирует изображение.
func decodeImage(ctx context.Context, blobs inference.BlobFetcher, input inference.ImageInput) (image.Image, error) {
	var data []byte
	switch {
	case input.Reader != nil:
		b, err := io.ReadAll(input.Reader)
		if err != nil {
			return nil, fmt.Errorf("%w: read: %v", inference.ErrInvalidImage, err)
		}
		data = b
	case input.SHA256 != "" && blobs != nil:
		b, err := blobs.Get(ctx, input.SHA256)
		if err != nil {
			return nil, fmt.Errorf("%w: blob %s: %v", inference.ErrInvalidImage, input.SHA256, err)
		}
		data = b
	default:
		return nil, fmt.Errorf("%w: no reader or blob store", inference.ErrInvalidImage)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: decode: %v", inference.ErrInvalidImage, err)
	}
	return img, nil
}

// letterboxTensor — letterbox-ресайз до 640x640 и NCHW-тензор 1x3x640x640,
// нормированный 0..1. Сохраняет пропорции: пустота заполняется серым 114
// (стандарт ultralytics). Детерминированно.
func letterboxTensor(img image.Image) (letterboxResult, error) {
	bounds := img.Bounds()
	sw, sh := float64(bounds.Dx()), float64(bounds.Dy())
	if sw <= 0 || sh <= 0 {
		return letterboxResult{}, fmt.Errorf("%w: empty image", inference.ErrInvalidImage)
	}
	scale := min(inputW/sw, inputH/sh)
	newW, newH := int(sw*scale+0.5), int(sh*scale+0.5)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}
	padX, padY := (inputW-newW)/2, (inputH-newH)/2

	resized := image.NewRGBA(image.Rect(0, 0, newW, newH))
	scaleNearest(resized, img)

	chw := make([]float32, 3*inputW*inputH)
	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			r, g, b, _ := resized.At(x, y).RGBA()
			idx := (padY+y)*inputW + (padX + x)
			chw[idx] = float32(r>>8) / 255
			chw[inputW*inputH+idx] = float32(g>>8) / 255
			chw[2*inputW*inputH+idx] = float32(b>>8) / 255
		}
	}
	// фон — серый 114 (0.447), как в ultralytics letterbox.
	const pad = float32(114.0 / 255.0)
	fillLetterboxPadding(chw, pad, padX, padY, newW, newH)

	return letterboxResult{
		data:   chw,
		params: letterboxParams{scale: scale, padX: padX, padY: padY, srcRect: bounds},
	}, nil
}

// scaleNearest — nearest-neighbor ресайз без внешних зависимостей:
// детерминированно; для детекции и гистограмм точности интерполяции достаточно.
func scaleNearest(dst *image.RGBA, src image.Image) {
	sb := src.Bounds()
	for y := 0; y < dst.Bounds().Dy(); y++ {
		sy := sb.Min.Y + y*sb.Dy()/dst.Bounds().Dy()
		for x := 0; x < dst.Bounds().Dx(); x++ {
			sx := sb.Min.X + x*sb.Dx()/dst.Bounds().Dx()
			dst.Set(x, y, src.At(sx, sy))
		}
	}
}

// fillLetterboxPadding заполняет области вне resized-картинки серым фоном.
func fillLetterboxPadding(chw []float32, pad float32, padX, padY, newW, newH int) {
	plane := inputW * inputH
	for c := 0; c < 3; c++ {
		base := c * plane
		// верх и низ
		for y := 0; y < padY; y++ {
			fillRow(chw[base+y*inputW:base+(y+1)*inputW], pad)
		}
		for y := padY + newH; y < inputH; y++ {
			fillRow(chw[base+y*inputW:base+(y+1)*inputW], pad)
		}
		// лево и право внутри вертикального диапазона картинки
		for y := padY; y < padY+newH; y++ {
			row := chw[base+y*inputW : base+(y+1)*inputW]
			fillRow(row[:padX], pad)
			fillRow(row[padX+newW:], pad)
		}
	}
}

func fillRow(row []float32, v float32) {
	for i := range row {
		row[i] = v
	}
}

// unletterbox переводит координаты бокса из тензора 640x640 обратно в
// координаты исходного изображения.
func unletterbox(b box, p letterboxParams) image.Rectangle {
	x0 := float64(b.x0-p.padX)/p.scale + float64(p.srcRect.Min.X)
	y0 := float64(b.y0-p.padY)/p.scale + float64(p.srcRect.Min.Y)
	x1 := float64(b.x1-p.padX)/p.scale + float64(p.srcRect.Min.X)
	y1 := float64(b.y1-p.padY)/p.scale + float64(p.srcRect.Min.Y)
	clamp := func(v float64, lo, hi int) int {
		if int(v) < lo {
			return lo
		}
		if int(v) > hi {
			return hi
		}
		return int(v)
	}
	return image.Rect(
		clamp(x0, p.srcRect.Min.X, p.srcRect.Max.X),
		clamp(y0, p.srcRect.Min.Y, p.srcRect.Max.Y),
		clamp(x1, p.srcRect.Min.X, p.srcRect.Max.X),
		clamp(y1, p.srcRect.Min.Y, p.srcRect.Max.Y),
	)
}

// makeCrops — кропы областей для предзаполнения трейтов: весь корпус
// (bounding box ± margin) и голова (верхняя часть бокса, эвристика).
func makeCrops(img image.Image, b image.Rectangle) Crops {
	crop := func(r image.Rectangle) image.Image {
		r = r.Intersect(img.Bounds())
		if r.Empty() {
			return img
		}
		out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		for y := 0; y < r.Dy(); y++ {
			for x := 0; x < r.Dx(); x++ {
				out.Set(x, y, img.At(r.Min.X+x, r.Min.Y+y))
			}
		}
		return out
	}
	mw := (b.Dx() * cropMarginPct) / 100
	mh := (b.Dy() * cropMarginPct) / 100
	body := crop(image.Rect(b.Min.X-mw, b.Min.Y-mh, b.Max.X+mw, b.Max.Y+mh))
	// Голова: верхняя доля бокса, центральные headWidthPct по ширине —
	// эвристика стоящей лошади; подтверждается человеком.
	headH := (b.Dy() * headFractionPct) / 100
	headW := (b.Dx() * headWidthPct) / 100
	offX := (b.Dx() - headW) / 2
	head := crop(image.Rect(b.Min.X+offX, b.Min.Y, b.Min.X+offX+headW, b.Min.Y+headH))
	return Crops{Body: body, Head: head, Box: b}
}

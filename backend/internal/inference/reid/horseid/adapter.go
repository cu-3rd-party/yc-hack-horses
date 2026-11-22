// Кропы для пайплайна horseid: тонкие обёртки над геометрией из crop.go.
// Тело — самый большой бокс с запасом BodyMargin, голова — HeadBox из точек.
package horseid

import "image"

// CropBody возвращает body-кроп: бокс тела с запасом BodyMargin, обрезанный
// по кадру, как PNG-байты для эмбеддера.
func CropBody(img image.Image, det *Detection) ([]byte, error) {
	box := BodyBox(det.BodyBox, img.Bounds().Dx(), img.Bounds().Dy())
	return CropPNG(img, box)
}

// CropHead возвращает head-кроп из det.HeadBox (nil — головы нет), как PNG-байты.
func CropHead(img image.Image, det *Detection) ([]byte, error) {
	if det.HeadBox == nil {
		return nil, nil
	}
	return CropPNG(img, *det.HeadBox)
}

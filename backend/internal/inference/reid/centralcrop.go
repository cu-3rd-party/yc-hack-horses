package reid

// Двухвариантный кроп — приём против фонового переобучения из
// zapovednik-public (ml/reid): каждый детект кодируется дважды —
//   raw     — исходный RGB-кроп бокса;
//   masked  — фон кропа залит серым (127,127,127) по сегмент-маске силуэта.
// Фьюжн-скоринг (fusion.go) обрабатывает варианты как независимые
// дескрипторы: z-нормировка per-query делает их шкалы сопоставимыми.
// Реализация написана с нуля на Go, см. NOTICE.md.

import "image"

// centralFraction — доля стороны центрального кропа, оставляемая при
// fallback-варианте masked без сегментации (срезает фон по краям бокса).
const centralFraction = 0.8

// centralVersion — отдельная версия эмбеддинга для fallback-варианта.
// Через Similarity она держится в отдельном пространстве поиска, чтобы
// fallback-вектора не смешивались с raw-векторами (см. IsSilhouette).
const centralVersion = "megadescriptor-l384-central-v1"

// centralRect — центральный кроп бокса: centralFraction от каждой стороны.
// Гарантирует непустой прямоугольник внутри исходного бокса.
func centralRect(box image.Rectangle) image.Rectangle {
	if box.Empty() {
		return box
	}
	dx := int(float64(box.Dx()) * (1 - centralFraction) / 2)
	dy := int(float64(box.Dy()) * (1 - centralFraction) / 2)
	r := image.Rect(box.Min.X+dx, box.Min.Y+dy, box.Max.X-dx, box.Max.Y-dy)
	if r.Empty() {
		return box
	}
	return r
}

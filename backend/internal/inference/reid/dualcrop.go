//go:build cgo

package reid

import (
	"context"
	"image"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

// EmbedDual — двухвариантное кодирование одного детекта: raw + masked
// (порт приёма zapovednik-public, см. centralcrop.go).
//
//   - raw: неизменённый RGB-кроп (BoxVersion) — существующее поведение;
//   - masked: фон залит серым по маске силуэта (Version, silhouette-пространство),
//     приём против фонового переобучения. Маска берётся из сегментера
//     (YOLO-seg/DINOv2-part маски, уже имеющиеся в ноде); если сегментера
//     нет — fallback: центральный кроп 80% бокса с отдельной версией
//     centralVersion (отдельное пространство поиска). Пороговый «фильтр фона»
//     не изобретается: без маски честно деградируем к центральному кропу.
func (e *Encoder) EmbedDual(ctx context.Context, src image.Image, box image.Rectangle, seg *Segmenter) (inference.EmbeddingResult, inference.EmbeddingResult, error) {
	raw, err := e.Embed(ctx, src, box)
	if err != nil {
		return inference.EmbeddingResult{}, inference.EmbeddingResult{}, err
	}
	if seg == nil {
		masked, err := e.EmbedCentral(ctx, src, box)
		return raw, masked, err
	}
	region, err := seg.Region(ctx, src, box, false)
	if err != nil {
		return raw, inference.EmbeddingResult{}, err
	}
	masked, err := e.EmbedRegion(ctx, region)
	if err != nil {
		return raw, inference.EmbeddingResult{}, err
	}
	return raw, masked, nil
}

// EmbedCentral — fallback-вариант masked без сегментации: центральный кроп
// 80% бокса, отдельная версия (не смешивается с raw через Similarity).
func (e *Encoder) EmbedCentral(ctx context.Context, src image.Image, box image.Rectangle) (inference.EmbeddingResult, error) {
	crop, err := cropForParts(src, centralRect(box))
	if err != nil {
		return inference.EmbeddingResult{}, err
	}
	return e.encode(ctx, crop, crop.Bounds(), centralVersion)
}

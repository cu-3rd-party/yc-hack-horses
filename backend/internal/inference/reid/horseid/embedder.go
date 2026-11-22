package horseid

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"path/filepath"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/modelassets"
	"github.com/disintegration/imaging"
)

// ArcEmbedder — реализация Embedder поверх DINOv2-B (ONNX) и ArcFace-проекций
// из export/heads_dinov2_<part>.f32. Путь к PNG/JPEG-кропу не нужен: кропы
// приходят PNG-байтами (crop.go) и декодируются здесь.
type ArcEmbedder struct {
	dino *Dino
	head *Projection
	body *Projection
}

// OpenArcEmbedder открывает DINOv2 из modelDir и загружает обе проекции.
func OpenArcEmbedder(modelDir string) (*ArcEmbedder, error) {
	dino, err := OpenDino(filepath.Join(modelDir, modelassets.DinoV2B14ONNX))
	if err != nil {
		return nil, err
	}
	head, err := LoadProjection(filepath.Join(modelDir, modelassets.HeadsHeadF32))
	if err != nil {
		_ = dino.Close()
		return nil, err
	}
	body, err := LoadProjection(filepath.Join(modelDir, modelassets.HeadsBodyF32))
	if err != nil {
		_ = dino.Close()
		return nil, err
	}
	return &ArcEmbedder{dino: dino, head: head, body: body}, nil
}

// Close останавливает ONNX-сессию DINOv2.
func (e *ArcEmbedder) Close() { e.dino.Close() }

// EmbedHead — проекция head-кропа: DINOv2 → ArcFace → нормализованный вектор 256.
func (e *ArcEmbedder) EmbedHead(crop []byte) ([]float64, error) {
	return e.embed(e.head, crop)
}

// EmbedBody — то же для body-кропа.
func (e *ArcEmbedder) EmbedBody(crop []byte) ([]float64, error) {
	return e.embed(e.body, crop)
}

func (e *ArcEmbedder) embed(proj *Projection, crop []byte) ([]float64, error) {
	img, err := decodeCropImage(crop)
	if err != nil {
		return nil, err
	}
	x, err := e.dino.Embed(context.Background(), img)
	if err != nil {
		return nil, err
	}
	out, err := proj.Apply(x)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// decodeCropImage декодирует PNG (crop.go) или JPEG-кроп в image.Image.
func decodeCropImage(crop []byte) (image.Image, error) {
	if len(crop) == 0 {
		return nil, fmt.Errorf("%w: пустой кроп", inference.ErrInvalidImage)
	}
	img, err := imaging.Decode(bytes.NewReader(crop), imaging.AutoOrientation(true))
	if err != nil {
		return nil, fmt.Errorf("%w: кроп не распознан", inference.ErrInvalidImage)
	}
	return img, nil
}

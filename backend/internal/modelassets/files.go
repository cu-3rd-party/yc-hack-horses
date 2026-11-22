package modelassets

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	DescriptorONNX       = "megadescriptor-l-384.onnx"
	DescriptorCheckpoint = "megadescriptor-l-384.bin"
	checkpointSHA        = "ccfe757f50f7984a115ffe00921cd4c09e260e645215463b23814586040227a3"
	checkpointURL        = "https://huggingface.co/BVRA/MegaDescriptor-L-384/resolve/33b3c6f4ee0c386a4126cc3dcd23843920613fa1/pytorch_model.bin"
	checkpointSize       = 1935177938
)

// Имена и SHA256 артефактов ReID-пайплайна horseid-dinov2-arcface-v2
// (HANDOFF_GO.md). ONNX-модели лежат ВНЕ git (слишком велики) — их отдаёт
// выпуск с базовым URL reidModelsReleaseURL; веса голов/банка (f32 + json,
// ~4 МБ) собраны тем же выпуском, чтобы прод-контейнеру не требовался git и
// действовал единый механизм доставки. SHA считаны из
// /root/k8s/src/models/reid-lab/*.onnx и infra/reid-lab/export/*.
const (
	Yolo11sONNX    = "yolo11s.onnx"
	VitPoseONNX    = "vitpose-plus-base-256x192.onnx"
	DinoV2B14ONNX  = "dinov2-b14-224.onnx"
	HeadsHeadF32   = "heads_dinov2_head.f32"
	HeadsBodyF32   = "heads_dinov2_body.f32"
	GalleryHeadF32 = "gallery_dinov2_head.f32"
	GalleryBodyF32 = "gallery_dinov2_body.f32"
	GalleryJSON    = "gallery.json"
	MetaJSON       = "meta.json"

	Yolo11sSHA     = "d34db73ed794a4e39f2fe7a768a8e4891eeaf270cd431e6c16a8c55375aaed61"
	VitPoseSHA     = "bbb7b9a60bcb23eb1c86dab120c880af2aaa1e7b18afb64a3d8cf109af31d6a9"
	DinoV2B14SHA   = "77229c4745536ef3ee85986585c7acddf6809a43c15755cc32e956bd6c602cc1"
	HeadsHeadSHA   = "34a35efdd6af5ca2e909125acef42605cb6789975460a26be8db5f1521a57c14"
	HeadsBodySHA   = "c217dc2c77dd29e273af2955381aeee7aace53d7070b4fbf0188a09eb448ee54"
	GalleryHeadSHA = "e655abf4b6625a38ae31efd9b66d08e060fb1edfa65fe4d4a525320661c1578b"
	GalleryBodySHA = "688ab4ec4c1c6891edff9556aef4a91dc2ac25ac97ff1a7e8f96eaa5b33d6e96"
	GalleryJSONSHA = "6785576d4f91fe5eaa5ce6f0a4ee9c608697314f2ff754a56e27a76d11308d02"
	MetaJSONSHA    = "68d6b57fe6ce09bad877fd7c67ef63f90837bd4728c966c6c211a7b0dfebd4af"

	// reidModelsReleaseURL — базовый URL выпуска с артефактами ReID (публичная
	// страница выпусков того же репозитория; отдельного выпуска для весов нет,
	// тег фиксирует версию файлов). INFER_MODEL_BASE_URL при необходимости
	// переопределяет источник.
	reidModelsReleaseURL = "https://git.cu3rd.ru/cu3rd/yc-hack-horses/releases/download/reid-models-v1"
)

// artifactSpec — описание одного скачиваемого файла модели.
type artifactSpec struct {
	id, name, sha, origin string
	size                  int64
}

// horseIDArtifacts возвращает набор артефактов horseid с пинами SHA256 и
// размерами (для прогресса загрузки). Порядок устойчив: он же порядок Status().
func horseIDArtifacts() []artifactSpec {
	return []artifactSpec{
		{"yolo11s", Yolo11sONNX, Yolo11sSHA, reidModelsReleaseURL + "/" + Yolo11sONNX, 38051784},
		{"vitpose", VitPoseONNX, VitPoseSHA, reidModelsReleaseURL + "/" + VitPoseONNX, 501965660},
		{"dinov2-b14", DinoV2B14ONNX, DinoV2B14SHA, reidModelsReleaseURL + "/" + DinoV2B14ONNX, 343032939},
		{"heads-dinov2-head", HeadsHeadF32, HeadsHeadSHA, reidModelsReleaseURL + "/" + HeadsHeadF32, 787456},
		{"heads-dinov2-body", HeadsBodyF32, HeadsBodySHA, reidModelsReleaseURL + "/" + HeadsBodyF32, 787456},
		{"gallery-dinov2-head", GalleryHeadF32, GalleryHeadSHA, reidModelsReleaseURL + "/" + GalleryHeadF32, 1140736},
		{"gallery-dinov2-body", GalleryBodyF32, GalleryBodySHA, reidModelsReleaseURL + "/" + GalleryBodyF32, 1140736},
		{"gallery-json", GalleryJSON, GalleryJSONSHA, reidModelsReleaseURL + "/" + GalleryJSON, 91653},
		{"meta-json", MetaJSON, MetaJSONSHA, reidModelsReleaseURL + "/" + MetaJSON, 566},
	}
}

var errChecksum = errors.New("unexpected model checksum")

type modelFile struct {
	path, sha string
}

func (file modelFile) verify() (int64, error) {
	f, err := os.Open(file.path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, err
	}
	if !MatchesSHA(file.sha, fmt.Sprintf("%x", h.Sum(nil))) {
		return 0, fmt.Errorf("%w: %s", errChecksum, filepath.Base(file.path))
	}
	return n, nil
}

// Keep recognizing an installed standalone ONNX export when switching download sources.
func installedFile(primary, alternative modelFile) (string, int64, error) {
	n, err := primary.verify()
	if err == nil || alternative.path == "" {
		return primary.path, n, err
	}
	n, otherErr := alternative.verify()
	if otherErr == nil || errors.Is(err, os.ErrNotExist) {
		return alternative.path, n, otherErr
	}
	return primary.path, 0, err
}

// VerifiedDescriptor returns the original ONNX export or the official BVRA checkpoint.
func VerifiedDescriptor(dir string) (string, error) {
	path, _, err := installedFile(
		modelFile{filepath.Join(dir, DescriptorONNX), DescriptorSHA},
		modelFile{filepath.Join(dir, DescriptorCheckpoint), checkpointSHA},
	)
	return path, err
}

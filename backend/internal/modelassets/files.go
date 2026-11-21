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
	if fmt.Sprintf("%x", h.Sum(nil)) != file.sha {
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

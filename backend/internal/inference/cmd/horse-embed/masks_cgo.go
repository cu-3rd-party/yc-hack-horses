//go:build cgo

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"os"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
	ort "github.com/yalue/onnxruntime_go"
)

// Restore masks for existing reports without invoking YOLO or MegaDescriptor.
func exportMasks(ctx context.Context, dir, lib string, input io.Reader, output io.Writer) error {
	ort.SetSharedLibraryPath(lib)
	if err := ort.InitializeEnvironment(); err != nil {
		return err
	}
	defer ort.DestroyEnvironment()
	model, err := reid.OpenSegmenter(dir)
	if err != nil {
		return err
	}
	defer model.Close()
	dec, enc := json.NewDecoder(input), json.NewEncoder(output)
	for {
		var row record
		err = dec.Decode(&row)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		data, err := os.ReadFile(row.File)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != row.SHA {
			return fmt.Errorf("image checksum differs: %s", row.File)
		}
		src, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return err
		}
		box := image.Rectangle{Min: image.Pt(row.Box[0], row.Box[1]), Max: image.Pt(row.Box[2], row.Box[3])}
		mask, err := model.Mask(ctx, src, box)
		if err != nil {
			return fmt.Errorf("%s: %w", row.File, err)
		}
		if err = enc.Encode(record{File: row.File, SHA: row.SHA, Box: row.Box, MaskPNG: &mask}); err != nil {
			return err
		}
	}
}

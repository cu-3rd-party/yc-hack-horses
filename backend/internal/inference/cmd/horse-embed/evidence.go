package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"os"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
)

func explainMasks(input io.Reader, output io.Writer) error {
	dec, enc := json.NewDecoder(input), json.NewEncoder(output)
	for {
		var row record
		err := dec.Decode(&row)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.MaskPNG == nil {
			return fmt.Errorf("%s: missing anatomical mask", row.File)
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
		evidence, err := reid.ExplainMask(src, box, *row.MaskPNG)
		if err != nil {
			return fmt.Errorf("%s: %w", row.File, err)
		}
		annotation, err := comparison.AnnotationFromMask(*row.MaskPNG, &evidence, box, src.Bounds())
		if err != nil {
			return fmt.Errorf("%s: %w", row.File, err)
		}
		if err := enc.Encode(struct {
			Annotation comparison.Annotation `json:"annotation"`
			File       string                `json:"file"`
			Traits     reid.TraitEvidence    `json:"traits"`
		}{annotation, row.File, evidence}); err != nil {
			return err
		}
	}
}

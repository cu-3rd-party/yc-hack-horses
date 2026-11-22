// Command horse-embed writes one versioned embedding per detected horse as JSONL.
// The optional gallery is a previous JSONL export. Candidates use visual cosine.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/yolo"
)

type candidate struct {
	File  string  `json:"file"`
	Box   [4]int  `json:"box"`
	Score float64 `json:"similarity"`
}
type record struct {
	CaptureTime      string              `json:"capture_time,omitempty"`
	File             string              `json:"file"`
	SHA              string              `json:"sha256"`
	Box              [4]int              `json:"box"`
	Confidence       float64             `json:"detection_confidence,omitempty"`
	Source           string              `json:"source"`
	Version          string              `json:"model_version,omitempty"`
	MaskPNG          *[]byte             `json:"mask_png,omitempty"`
	Traits           *reid.TraitEvidence `json:"traits,omitempty"`
	MeasurementError string              `json:"measurement_error,omitempty"`
	Vector           []float32           `json:"vector,omitempty"`
	Candidates       []candidate         `json:"candidates,omitempty"`
	VisualCandidates []candidate         `json:"visual_candidates,omitempty"`
}

func (r record) embedding() inference.EmbeddingResult {
	return inference.EmbeddingResult{ModelVersion: r.Version, Vector: r.Vector}
}

func loadGallery(path string) ([]record, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []record
	dec := json.NewDecoder(f)
	for {
		var r record
		err := dec.Decode(&r)
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, err
		}
		if r.Source == "missed" {
			continue
		}
		if err := reid.Validate(r.embedding()); err != nil {
			return nil, fmt.Errorf("gallery %s: %w", r.File, err)
		}
		rows = append(rows, r)
	}
}

func (r record) candidates(gallery []record) ([]candidate, error) {
	var candidates []candidate
	for _, g := range gallery {
		if g.SHA == r.SHA {
			continue
		}
		score, err := reid.Similarity(r.embedding(), g.embedding())
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate{g.File, g.Box, score})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	return candidates[:min(5, len(candidates))], nil
}

func parseBox(s string) (*image.Rectangle, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return nil, fmt.Errorf("-box requires x0,y0,x1,y1")
	}
	var a [4]int
	for i, str := range parts {
		v, err := strconv.Atoi(str)
		if err != nil {
			return nil, err
		}
		a[i] = v
	}
	r := image.Rectangle{Min: image.Pt(a[0], a[1]), Max: image.Pt(a[2], a[3])}
	if r.Empty() {
		return nil, fmt.Errorf("empty box %s", s)
	}
	return &r, nil
}

func run(ctx context.Context) error {
	dir := flag.String("models", os.Getenv("INFER_REID_MODEL_DIR"), "directory containing MegaDescriptor, DINOv2 and horse part heads")
	det := flag.String("detector", os.Getenv("INFER_MODEL_PATH"), "COCO detector ONNX path (YOLOv8n or YOLO26x)")
	lib := flag.String("runtime", os.Getenv("INFER_RUNTIME_LIB"), "ONNX Runtime shared library")
	galleryPath := flag.String("gallery", "", "previous JSONL export to compare with")
	boxText := flag.String("box", "", "single manually supplied box x0,y0,x1,y1 in decoded image coordinates (one input photo)")
	queries := flag.String("queries", "", "compare an existing query JSONL with -gallery using visual cosine")
	includeMasks := flag.Bool("masks", false, "include colour-indexed anatomical PNGs for detected horses")
	boxMasks := flag.Bool("mask-boxes", false, "read previous photo hashes and boxes as JSONL on stdin; output masks only")
	explain := flag.Bool("explain-masks", false, "read saved indexed masks and photo hashes as JSONL on stdin; output physical measurements and overlays")
	flag.Parse()
	if *explain {
		return explainMasks(os.Stdin, os.Stdout)
	}
	if *boxMasks {
		if *dir == "" || *lib == "" || *queries != "" || *galleryPath != "" || *boxText != "" || flag.NArg() != 0 {
			return fmt.Errorf("-mask-boxes requires -models, -runtime and boxes on stdin")
		}
		return exportMasks(ctx, *dir, *lib, os.Stdin, os.Stdout)
	}
	if *queries != "" {
		if *galleryPath == "" || flag.NArg() != 0 || *boxText != "" {
			return fmt.Errorf("-queries requires -gallery and no image arguments or -box")
		}
		gallery, err := loadGallery(*galleryPath)
		if err != nil {
			return err
		}
		rows, err := loadGallery(*queries)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		for _, row := range rows {
			row.MaskPNG = nil
			row.Candidates, err = row.candidates(gallery)
			if err != nil {
				return err
			}
			row.VisualCandidates = row.Candidates // compatibility with previous JSONL consumers
			if err = enc.Encode(row); err != nil {
				return err
			}
		}
		return nil
	}
	if *dir == "" || *det == "" || *lib == "" || flag.NArg() == 0 {
		return fmt.Errorf("provide -models, -detector, -runtime and one or more image paths")
	}
	region, err := parseBox(*boxText)
	if err != nil {
		return err
	}
	if region != nil && flag.NArg() != 1 {
		return fmt.Errorf("-box applies to exactly one photo")
	}
	gallery, err := loadGallery(*galleryPath)
	if err != nil {
		return err
	}
	engine := yolo.New(yolo.Config{ModelPath: *det, RuntimeLibPath: *lib, ReIDModelDir: *dir, IncludeMasks: *includeMasks})
	defer engine.Close()
	enc := json.NewEncoder(os.Stdout)
	for _, file := range flag.Args() {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		r := record{CaptureTime: comparison.CaptureTime(data), File: file, SHA: fmt.Sprintf("%x", sha256.Sum256(data)), Source: "automatic"}
		rows, err := engine.EmbedInstances(ctx, inference.ImageInput{Reader: bytes.NewReader(data)}, region)
		if errors.Is(err, inference.ErrNoAnimalDetected) {
			r.Source = "missed"
			if err := enc.Encode(r); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		if region != nil {
			r.Source = "provided_box"
		}
		for _, row := range rows {
			r.Confidence = row.Confidence
			r.Box = [4]int{row.Box.Min.X, row.Box.Min.Y, row.Box.Max.X, row.Box.Max.Y}
			r.Version, r.Vector = row.Embedding.ModelVersion, row.Embedding.Vector
			r.Traits, r.MeasurementError = row.Traits, row.MeasurementError
			if *includeMasks {
				r.MaskPNG = &row.MaskPNG
			}
			r.Candidates, err = r.candidates(gallery)
			if err != nil {
				return err
			}
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

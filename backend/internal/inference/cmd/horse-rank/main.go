// Command horse-rank ranks horse-embed JSONL against named reference crops using the journal's comparison algorithm.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
)

type reference struct {
	Capture string    `json:"capture_time,omitempty"`
	Day     string    `json:"day,omitempty"`
	SHA     string    `json:"sha256"`
	Name    string    `json:"name"`
	Version string    `json:"model_version"`
	Vector  []float64 `json:"vector"`
}

type query struct {
	Capture    string    `json:"capture_time,omitempty"`
	Day        string    `json:"day,omitempty"`
	MaskPNG    string    `json:"mask_png,omitempty"`
	SHA        string    `json:"sha256"`
	Source     string    `json:"source"`
	Box        [4]int    `json:"box"`
	Confidence float64   `json:"detection_confidence"`
	Version    string    `json:"model_version"`
	Vector     []float64 `json:"vector"`
}

type ranked struct {
	MaskPNG    string                 `json:"mask_png,omitempty"`
	SHA        string                 `json:"sha256"`
	Source     string                 `json:"source"`
	Box        [4]int                 `json:"box"`
	Confidence float64                `json:"detection_confidence"`
	Candidates []comparison.Candidate `json:"candidates"`
}

func run() error {
	path := flag.String("gallery", "", "named reference descriptors in JSON")
	flag.Parse()
	data, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	var gallery []reference
	if err := json.Unmarshal(data, &gallery); err != nil {
		return err
	}
	refs := make([]comparison.Reference, 0, len(gallery))
	versions := map[string]bool{}
	for _, r := range gallery {
		// megadescriptor-версии привязаны к reid.Dimension; cushki-версии
		// (cushki-*) несут динамическую размерность модели — валидна любая
		// положительная длина, кросс-версии отсекает проверка versions ниже.
		if !reid.ValidVector(r.Version, len(r.Vector)) || (!reid.IsSilhouette(r.Version) && r.Version != reid.BoxVersion && !reid.IsCushkiVersion(r.Version)) || r.Name == "" || r.SHA == "" {
			return fmt.Errorf("invalid gallery reference %q", r.SHA)
		}
		versions[r.Version] = true
		if r.Capture == "" {
			r.Capture = r.Day
		}
		refs = append(refs, comparison.Reference{
			HorseID: r.Name, Name: r.Name, PhotoID: r.SHA,
			Descriptor: comparison.Descriptor{Version: r.Version, Vector: r.Vector, CaptureTime: r.Capture},
		})
	}
	dec, enc := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var q query
		if err := dec.Decode(&q); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		row := ranked{MaskPNG: q.MaskPNG, SHA: q.SHA, Source: q.Source, Box: q.Box, Confidence: q.Confidence, Candidates: []comparison.Candidate{}}
		if q.Source != "missed" {
			if (!reid.IsSilhouette(q.Version) && q.Version != reid.BoxVersion && !reid.IsCushkiVersion(q.Version)) || !reid.ValidVector(q.Version, len(q.Vector)) {
				return fmt.Errorf("invalid query descriptor %q", q.SHA)
			}
			if len(refs) > 0 && !versions[q.Version] {
				return fmt.Errorf("gallery has no descriptors for %s; re-encode references with the query model", q.Version)
			}
			eligible := make([]comparison.Reference, 0, len(refs))
			for _, r := range refs {
				if r.PhotoID != q.SHA {
					eligible = append(eligible, r)
				}
			}
			if q.Capture == "" {
				q.Capture = q.Day
			}
			row.Candidates = comparison.Rank(comparison.Descriptor{Version: q.Version, Vector: q.Vector, CaptureTime: q.Capture}, eligible, 5)
		}
		if err := enc.Encode(row); err != nil {
			return err
		}
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

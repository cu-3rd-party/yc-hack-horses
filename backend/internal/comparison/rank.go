package comparison

import (
	"math"
	"sort"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
)

// Reference — подтверждённое фото-эталон для ранжирования.
// InvalidatedAt выставляет источник, знающий о мягком удалении фото
// (blob.deleted): такие эталоны не участвуют в поиске. Единственный текущий
// производитель Reference — sqlstore.References, который уже фильтрует по
// invalidated_at; проверка в Rank — предохранитель defence-in-depth на случай
// будущих источников. Иначе помеченный эталон мог бы победить по косинусу и
// попасть в BestPhotoID, а клиент получил бы ссылку на несуществующий блоб.
type Reference struct {
	HorseID, Name, PhotoID, SHA string
	NeedsCapture                bool
	InvalidatedAt               *time.Time
	Descriptor
}

// searchable сообщает, что эталон не помечен как удалённый.
func (r Reference) searchable() bool { return r.InvalidatedAt == nil }

// unit returns a normalized vector, or nil for a missing/degenerate embedding.
// Scaling before taking the norm avoids overflow for finite model outputs.
func unit(v []float64) []float64 {
	scale := 0.0
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		scale = math.Max(scale, math.Abs(x))
	}
	if scale == 0 {
		return nil
	}
	u := make([]float64, len(v))
	sum := 0.0
	for i, x := range v {
		u[i] = x / scale
		sum += u[i] * u[i]
	}
	norm := math.Sqrt(sum)
	for i := range u {
		u[i] /= norm
	}
	return u
}

// Date only and local ISO timestamps share the same YYYY-MM-DD prefix.
func calendarSeason(capture string) int {
	if len(capture) < 10 {
		return -1
	}
	d, err := time.Parse(time.DateOnly, capture[:10])
	if err != nil {
		return -1
	}
	return int(d.Month()) % 12 / 3
}

func dotUnit(a, b []float64) float64 {
	var sum float64
	for i, x := range a {
		sum += x * b[i]
	}
	return max(-1., min(1., sum))
}

func referenceKey(r Reference) string {
	if r.SHA != "" {
		return r.SHA
	}
	return r.PhotoID
}

// A repeated confirmation of the same photo contributes only one embedding.
func recentReferences(rows []Reference, season int) ([]Reference, bool) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.CaptureTime != b.CaptureTime {
			return a.CaptureTime > b.CaptureTime
		}
		if referenceKey(a) != referenceKey(b) {
			return referenceKey(a) < referenceKey(b)
		}
		return a.PhotoID > b.PhotoID
	})
	var bins [5][]Reference // last bin is reserved for legacy undated photos
	seen := map[string]bool{}
	for _, r := range rows {
		k := referenceKey(r)
		if seen[k] {
			continue
		}
		seen[k] = true
		s := calendarSeason(r.CaptureTime)
		if s < 0 {
			s = 4
		}
		if len(bins[s]) < 5 {
			bins[s] = append(bins[s], r)
		}
	}
	if season >= 0 && len(bins[season]) > 0 {
		return bins[season], false
	}
	var fallback []Reference
	for _, bin := range bins[:4] {
		fallback = append(fallback, bin...)
	}
	if len(fallback) == 0 {
		fallback = bins[4]
	}
	return fallback, true
}

func Rank(query Descriptor, references []Reference, k int) []Candidate {
	q := unit(query.Vector)
	useAdapter := query.Version == reid.BoxVersion
	if useAdapter {
		q = adaptBox(q)
	}
	if k < 1 || q == nil {
		return []Candidate{}
	}
	groups := map[string][]Reference{}
	for _, r := range references {
		if !r.searchable() || r.Version != query.Version || len(r.Vector) != len(q) {
			continue
		}
		r.Vector = unit(r.Vector)
		if r.Vector != nil {
			groups[r.HorseID] = append(groups[r.HorseID], r)
		}
	}
	requested := calendarSeason(query.CaptureTime)
	out := make([]Candidate, 0, len(groups))
	for id, rows := range groups {
		refs, fallback := recentReferences(rows, requested)
		sum := make([]float64, len(q))
		candidate := Candidate{HorseID: id, Name: refs[0].Name, ReferenceCount: len(refs), SeasonFallback: fallback}
		for j, r := range refs {
			if useAdapter {
				r.Vector = adaptBox(r.Vector)
				refs[j].Vector = r.Vector
			}
			for i, x := range r.Vector {
				sum[i] += x
			}
		}
		centroid := unit(sum)
		if centroid == nil {
			continue
		}
		candidate.Similarity = dotUnit(q, centroid)
		anchor := q
		if useAdapter {
			// The example photograph represents this horse's seasonal centroid.
			anchor = centroid
		}
		best := -2.
		for _, r := range refs {
			score := dotUnit(anchor, r.Vector)
			if score > best || (!useAdapter && score == best && r.PhotoID < candidate.BestPhotoID) {
				best = score
				candidate.BestPhotoID = r.PhotoID
			}
		}
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Similarity == out[j].Similarity {
			return out[i].HorseID < out[j].HorseID
		}
		return out[i].Similarity > out[j].Similarity
	})
	return out[:min(len(out), k)]
}

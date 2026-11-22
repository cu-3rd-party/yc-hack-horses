package comparison

import (
	"math"
	"sort"
	"time"
)

// Reference — подтверждённое фото-эталон для ранжирования.
// InvalidatedAt выставляет источник, знающий о мягком удалении фото
// (blob.deleted): такие эталоны не участвуют в поиске. Единственный текущий
// производитель Reference — sqlstore.References, который уже фильтрует по
// invalidated_at; проверка в Rank — предохранитель defence-in-depth на случай
// будущих источников. Иначе помеченный эталон мог бы победить по косинусу и
// попасть в BestPhotoID, а клиент получил бы ссылку на несуществующий блоб.
type Reference struct {
	HorseID, Name, PhotoID string
	InvalidatedAt          *time.Time
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

func Rank(query Descriptor, references []Reference, k int) []Candidate {
	// Диспетчер версий: horseid-дескриптор (голова+тело) идёт в отдельную
	// реализацию, остальные версии (megadescriptor, seasonal-адаптер) —
	// прежним путём, без изменений.
	if query.Version == horseidVersion {
		return rankHorseID(query, references, k)
	}
	q := unit(query.Vector)
	if k < 1 || len(q) == 0 {
		return []Candidate{}
	}
	best := make(map[string]Candidate)
	for _, ref := range references {
		// Удалённое фото не участвует в поиске: его вектор не даёт ни очков,
		// ни BestPhotoID. Для той же лошади остаётся живое соседнее фото.
		if !ref.searchable() {
			continue
		}
		if query.Version != ref.Version || len(q) != len(ref.Vector) {
			continue
		}
		v := unit(ref.Vector)
		if v == nil {
			continue
		}
		var score float64
		for i := range q {
			score += q[i] * v[i]
		}
		score = max(-1.0, min(score, 1.0))
		old, ok := best[ref.HorseID]
		if !ok || score > old.Similarity || (score == old.Similarity && ref.PhotoID < old.BestPhotoID) {
			best[ref.HorseID] = Candidate{HorseID: ref.HorseID, Name: ref.Name, BestPhotoID: ref.PhotoID, Similarity: score}
		}
	}
	out := make([]Candidate, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Similarity == out[j].Similarity {
			return out[i].HorseID < out[j].HorseID
		}
		return out[i].Similarity > out[j].Similarity
	})
	return out[:min(len(out), k)]
}

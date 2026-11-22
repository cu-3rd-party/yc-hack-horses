package horseid

import (
	"math"
	"sort"
)

// HorseScore — результат ранжирования одной лошади банка.
// Head/Body — агрегированный по top2 счёт по части; HasHead/HasBody отмечают,
// что у лошади есть хотя бы одно фото с этой частью (иначе часть отсутствует).
type HorseScore struct {
	Horse   string
	Score   float64 // слияние head/body; при отсутствии одной части берётся другая
	Head    float64
	Body    float64
	HasHead bool
	HasBody bool
	Pct     float64 // процент softmax(Temp*score) по всем лошадям (не калиброван)
}

// Rank ранжирует запрос по банку:
//   - косинус (скалярное произведение нормализованных векторов) по каждой части
//     с каждым фото банка;
//   - счёт лошади по части = среднее двух лучших сходств среди её фото (top2;
//     одно фото — само значение; нет фото с частью — часть отсутствует);
//   - итоговый score = WeightHead*head + (1-WeightHead)*body, при отсутствии
//     одной части берётся другая;
//   - проценты = softmax с температурой Temp по всем лошадям (max вычитается
//     перед exp для устойчивости).
//
// Результат отсортирован по убыванию score (при равенстве — по имени).
func (g *Gallery) Rank(e Embedding) []HorseScore {
	w := g.Config.WeightHead
	scores := make([]HorseScore, 0, len(g.Horses))
	for _, h := range g.Horses {
		idx := g.byHorse[h]
		var headVals, bodyVals []float64
		if e.HasHead {
			headVals = g.collectSims(idx, e.Head, func(p GalleryPhoto) []float64 { return p.Head })
		}
		if e.HasBody {
			bodyVals = g.collectSims(idx, e.Body, func(p GalleryPhoto) []float64 { return p.Body })
		}
		hs := HorseScore{Horse: h}
		hs.Head, hs.HasHead = aggTop2(headVals)
		hs.Body, hs.HasBody = aggTop2(bodyVals)
		switch {
		case hs.HasHead && hs.HasBody:
			hs.Score = w*hs.Head + (1-w)*hs.Body
		case hs.HasHead:
			hs.Score = hs.Head
		case hs.HasBody:
			hs.Score = hs.Body
		default:
			hs.Score = 0
		}
		scores = append(scores, hs)
	}

	// softmax с температурой Temp (max вычитается для устойчивости).
	maxScore := math.Inf(-1)
	for _, s := range scores {
		if s.Score > maxScore {
			maxScore = s.Score
		}
	}
	var sum float64
	for i := range scores {
		scores[i].Pct = math.Exp(g.Config.Temp * (scores[i].Score - maxScore))
		sum += scores[i].Pct
	}
	if sum > 0 {
		for i := range scores {
			scores[i].Pct = scores[i].Pct / sum * 100
		}
	}

	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].Score != scores[j].Score {
			return scores[i].Score > scores[j].Score
		}
		return scores[i].Horse < scores[j].Horse
	})
	return scores
}

// collectSims считает косинус запроса со всеми фото лошади, у которых есть часть.
func (g *Gallery) collectSims(idx []int, query []float64, part func(GalleryPhoto) []float64) []float64 {
	out := make([]float64, 0, len(idx))
	for _, i := range idx {
		if ref := part(g.Photos[i]); ref != nil {
			out = append(out, dot(query, ref))
		}
	}
	return out
}

// aggTop2 возвращает среднее двух наибольших значений; при одном — само значение.
// Пустой срез => отсутствие (ok=false).
func aggTop2(v []float64) (float64, bool) {
	switch len(v) {
	case 0:
		return 0, false
	case 1:
		return v[0], true
	}
	max1, max2 := math.Inf(-1), math.Inf(-1)
	for _, x := range v {
		switch {
		case x > max1:
			max2, max1 = max1, x
		case x > max2:
			max2 = x
		}
	}
	return (max1 + max2) / 2, true
}

// dot — скалярное произведение; длины обрезаются по меньшей для устойчивости.
func dot(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var s float64
	for i := 0; i < n; i++ {
		s += a[i] * b[i]
	}
	return s
}

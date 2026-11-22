package comparison

import (
	"sort"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid/horseid"
)

// horseidVersion — версия дескриптора horseid-пайплайна. Значение берётся
// импортом из контракта (horseid/contract.go), чтобы не разъезжалось; если
// когда-нибудь появится циклический импорт comparison ⇄ horseid — продублировать
// литералом "horseid-dinov2-arcface-v2" с комментарием о синхронизации.
const horseidVersion = horseid.Version

// rankHorseID — ранжирование запроса версии horseid-dinov2-arcface-v2.
//
// Дескриптор этой версии — конкатенация двух L2-нормированных половин длины
// horseid.EmbedDim: [0:EmbedDim) — head, [EmbedDim:2*EmbedDim) — body
// (см. HANDOFF_GO.md, раздел «Как встроить в бэкенд», вариант 1).
// Отсутствие части кодируется нулевым вектором половины: NaN в сравнение не
// допускается (в этой версии эталон comparison не бывает NaN), а `unit` от
// нулевой половины возвращает nil, что и означает «части нет».
//
// Сходство считается по частям, как в эталоне infra/reid-lab/demo_app.py:
//   - сходство запроса с конкретным эталонным фото по части — косинус её половин;
//   - счёт лошади по части — среднее двух лучших сходств по её эталонам
//     (agg=top2 из meta.json; одно фото — само значение);
//   - итог = WeightHead*head + (1-WeightHead)*body; при отсутствии одной части
//     берётся другая, при отсутствии обеих лошадь не участвует.
//
// Проценты softmax(Temp) НЕ считаются здесь: это уровень вывода, а не Rank.
// Наружу отдаётся сырой score в поле Similarity. BestPhotoID — эталон лошади с
// лучшим per-photo слиянием head/body (та же величина, что demo_app показывает
// в списке «лучших фото»).
func rankHorseID(query Descriptor, references []Reference, k int) []Candidate {
	full := 2 * horseid.EmbedDim
	if k < 1 || len(query.Vector) != full {
		return []Candidate{}
	}
	qHead := unit(query.Vector[:horseid.EmbedDim])
	qBody := unit(query.Vector[horseid.EmbedDim:full])
	if qHead == nil && qBody == nil {
		return []Candidate{}
	}

	// Накопитель по лошади: сходства по частям нужны для top2, bestPer/bestID —
	// для BestPhotoID.
	type horse struct {
		name     string
		bestID   string
		bestPer  float64
		hasBest  bool
		headSims []float64
		bodySims []float64
	}
	byHorse := make(map[string]*horse, len(references))
	for _, ref := range references {
		// Удалённое фото не участвует в поиске: его вектор не даёт ни очков,
		// ни BestPhotoID (см. Reference.searchable).
		if !ref.searchable() {
			continue
		}
		if ref.Version != query.Version || len(ref.Vector) != full {
			continue
		}
		var (
			hSim, bSim float64
			hasH, hasB bool
		)
		if qHead != nil {
			if rh := unit(ref.Vector[:horseid.EmbedDim]); rh != nil {
				hSim, hasH = dot(qHead, rh), true
			}
		}
		if qBody != nil {
			if rb := unit(ref.Vector[horseid.EmbedDim:full]); rb != nil {
				bSim, hasB = dot(qBody, rb), true
			}
		}
		if !hasH && !hasB {
			// Ни одна половина эталона не сравнима — фото бесполезно.
			continue
		}
		// Per-photo слияние (как per_photo в demo_app) — только для выбора
		// самого показательного фото лошади.
		per := blend(hSim, hasH, bSim, hasB)

		acc, ok := byHorse[ref.HorseID]
		if !ok {
			acc = &horse{name: ref.Name}
			byHorse[ref.HorseID] = acc
		}
		if hasH {
			acc.headSims = append(acc.headSims, hSim)
		}
		if hasB {
			acc.bodySims = append(acc.bodySims, bSim)
		}
		if !acc.hasBest || per > acc.bestPer || (per == acc.bestPer && ref.PhotoID < acc.bestID) {
			acc.bestID, acc.bestPer, acc.hasBest = ref.PhotoID, per, true
		}
	}

	out := make([]Candidate, 0, len(byHorse))
	for id, acc := range byHorse {
		hScore, hasH := top2Mean(acc.headSims)
		bScore, hasB := top2Mean(acc.bodySims)
		if !hasH && !hasB {
			continue
		}
		score := blend(hScore, hasH, bScore, hasB)
		score = max(-1.0, min(score, 1.0))
		out = append(out, Candidate{HorseID: id, Name: acc.name, BestPhotoID: acc.bestID, Similarity: score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Similarity == out[j].Similarity {
			return out[i].HorseID < out[j].HorseID
		}
		return out[i].Similarity > out[j].Similarity
	})
	return out[:min(len(out), k)]
}

// blend сводит сходство head и body в итоговый score: WeightHead*head +
// (1-WeightHead)*body, а при отсутствии одной части — имеющуюся. Вызывается
// только когда хотя бы одна часть есть.
func blend(h float64, hasH bool, b float64, hasB bool) float64 {
	switch {
	case hasH && hasB:
		return horseid.WeightHead*h + (1-horseid.WeightHead)*b
	case hasH:
		return h
	default:
		return b
	}
}

// top2Mean возвращает среднее двух наибольших значений (или единственное,
// если оно одно) и признак, что хотя бы одно значение было.
func top2Mean(v []float64) (float64, bool) {
	if len(v) == 0 {
		return 0, false
	}
	first := v[0]
	second := 0.0
	has := false
	for _, x := range v[1:] {
		switch {
		case x > first:
			first, second, has = x, first, true
		case !has || x > second:
			second, has = x, true
		}
	}
	if !has {
		return first, true
	}
	return (first + second) / 2, true
}

// dot — скалярное произведение; обе части заранее нормированы unit.
func dot(a, b []float64) float64 {
	sum := 0.0
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

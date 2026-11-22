// cushki.go — скоринг CUshki v6 по галерее: косинус по эмбеддингам, опциональная
// калибровка матричной логистической головой из fusion_config.json и open-set
// порог new_threshold (0.3 в релизном конфиге). Порт семантики ml/serve/
// model_server.py проекта zapovednik-public (вероятности класса = softmax
// линейной головы над сшитым признаком); реализация написана с нуля, см. NOTICE.md.
//
// Ранжирование:
//   - оценка личности = максимум косинусной близости по её кропам галереи
//     (agg=max, как в fusion.IdentityScores);
//   - если все личности галереи входят в классы головы, итоговый порядок —
//     по откалиброванной вероятности P(класс=личность | признак запроса);
//     иначе порядок остаётся косинусным, а вероятности присоединяются где
//     определены (голова — классификатор над известными ей особями).
package reid

import (
	"fmt"
	"math"
	"sort"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

// CushkiGalleryScore — скор одной личности галереи.
type CushkiGalleryScore struct {
	// Cosine — максимум косинусной близости по кропам личности.
	Cosine float64
	// Probability — откалиброванная вероятность головы; NaN, если личность
	// не входит в классы головы (или голова неприменима).
	Probability float64
	// Hits — число кропов личности в галерее.
	Hits int
}

// cushkiSimilarity — косинус в cushki-пространстве: векторы хранятся
// единичными, но норму проверяем (защита от деградата).
func cushkiSimilarity(a, b []float64) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("reid: incompatible vector lengths %d and %d", len(a), len(b))
	}
	var da, db, dot float64
	for i := range a {
		da += a[i] * a[i]
		db += b[i] * b[i]
		dot += a[i] * b[i]
	}
	if da == 0 || db == 0 {
		return 0, fmt.Errorf("reid: invalid visual vector")
	}
	score := dot / math.Sqrt(da*db)
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0, fmt.Errorf("reid: invalid cosine")
	}
	return min(1, max(-1, score)), nil
}

// CushkiRankGallery ранжирует галерею для одного запроса.
//
//	query — сшитый признак запроса (CushkiConfig.Features) или вектор одного
//	кропа; gallery — признаки кропов галереи; ids — личность каждого кропа;
//	identities — уникальные личности (порядок результата повторяет его).
//
// Возвращает скоры в порядке identities. Калибровка применяется, если задан
// head и размерность признака совпадает; личности вне классов головы получают
// NaN в Probability.
func CushkiRankGallery(head *CushkiConfig, query []float64, gallery [][]float64, ids, identities []string) ([]CushkiGalleryScore, error) {
	out := make([]CushkiGalleryScore, len(identities))
	for qi, id := range identities {
		out[qi] = CushkiGalleryScore{Probability: math.NaN()}
		for gi, g := range gallery {
			if ids[gi] != id {
				continue
			}
			sim, err := cushkiSimilarity(query, g)
			if err != nil {
				return nil, err
			}
			out[qi].Hits++
			if sim > out[qi].Cosine {
				out[qi].Cosine = sim
			}
		}
	}
	if head != nil {
		probs, _ := head.CalibratedScores(identities, query)
		for i := range out {
			out[i].Probability = probs[i]
		}
	}
	return out, nil
}

// CushkiOrder возвращает порядок личностей: по вероятности головы, если все
// определены, иначе по косинусу. Возвращает индексы в identities.
func CushkiOrder(scores []CushkiGalleryScore) []int {
	byProb := true
	for _, s := range scores {
		if math.IsNaN(s.Probability) {
			byProb = false
			break
		}
	}
	idx := make([]int, len(scores))
	for i := range idx {
		idx[i] = i
	}
	key := func(i int) float64 {
		if byProb {
			return scores[i].Probability
		}
		return scores[i].Cosine
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ka, kb := key(idx[a]), key(idx[b])
		if ka == kb {
			return idx[a] < idx[b]
		}
		return ka > kb
	})
	return idx
}

// CushkiIsNewSuspected — open-set: голова задана, признак применим и
// вероятность лучшего класса ниже порога. Без головы (или при несовпадении
// размерности) «новая особь» не объявляется — дефолтное поведение прежнее.
func CushkiIsNewSuspected(head *CushkiConfig, query []float64) bool {
	if head == nil {
		return false
	}
	p := head.Probabilities(query)
	if p == nil {
		return false
	}
	best := math.Inf(-1)
	for _, v := range p {
		best = max(best, v)
	}
	return head.IsNewSuspected(best)
}

// CushkiQueryFeature собирает признак запроса из эмбеддингов члена ансамбля.
// Согласует версии: raw и masked должны быть одного пространства CUshki.
func CushkiQueryFeature(head *CushkiConfig, raw, masked inference.EmbeddingResult) ([]float64, error) {
	if !IsCushkiVersion(raw.ModelVersion) || raw.ModelVersion != masked.ModelVersion {
		return nil, fmt.Errorf("reid: cushki: incompatible embedding versions %q and %q", raw.ModelVersion, masked.ModelVersion)
	}
	if head != nil && (len(raw.Vector) != head.MemberDim() || len(masked.Vector) != head.MemberDim()) {
		return nil, fmt.Errorf("reid: cushki: embedding dim %d/%d does not match head member dim %d",
			len(raw.Vector), len(masked.Vector), head.MemberDim())
	}
	if len(raw.Vector) != len(masked.Vector) {
		return nil, fmt.Errorf("reid: cushki: raw/masked dim mismatch %d != %d", len(raw.Vector), len(masked.Vector))
	}
	if head != nil {
		return head.Features(raw.Vector, masked.Vector)
	}
	// Без головы признак не нужен: скоринг идёт по эмбеддингам членов.
	return nil, nil
}

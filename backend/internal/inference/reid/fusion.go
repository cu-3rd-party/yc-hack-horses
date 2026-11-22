package reid

// Скоринг, фьюжн и калибровка — порт семантики ml/reid/matching.py проекта
// zapovednik-public (https://github.com/Oimasi/zapovednik-public, «Особь»,
// команда CUshki). Код оригинала не копировался (лицензии нет): реализация
// написана с нуля на Go, воспроизводятся формулы и соглашения. См. NOTICE.md.
//
// Терминология оригинала:
//   gallery — кропы с известной личностью (референсный архив);
//   query   — новый кроп, который нужно опознать.
// Оценка личности для запроса = максимум косинусной близости по кропам этой
// личности (опционально среднее top-2 — устойчивее к единичному «удачному»
// совпадению).

import "math"

// Способы агрегации совпадений по кропам одной личности.
const (
	AggMax  = "max"
	AggTop2 = "top2"
)

// zScoreEpsilon и zFallback защищают от деления на ноль и подставляют
// нейтральные значения вместо -Inf перед логистической калибровкой —
// те же константы, что в pair_features оригинала.
const (
	zScoreEpsilon = 1e-6
	zFallback     = -5.0
	gapFallback   = 2.0
)

// IdentityScores сводит матрицу попарных близостей (Q x G) к оценкам по
// личностям (Q x I): максимум (или среднее top-2) по кропам личности.
// allowed: false запрещает сравнение запроса с данным кропом галереи
// (например, side-aware фильтр); nil означает «всё разрешено».
// Личность без разрешённых кропов получает -Inf.
func IdentityScores(sim [][]float64, galIDs, identities []string, allowed [][]bool, agg string) [][]float64 {
	out := make([][]float64, len(sim))
	for qi := range sim {
		out[qi] = make([]float64, len(identities))
		for ii, ident := range identities {
			best := math.Inf(-1)
			second := math.Inf(-1)
			for gi, id := range galIDs {
				if id != ident {
					continue
				}
				if allowed != nil && !allowed[qi][gi] {
					continue
				}
				v := sim[qi][gi]
				switch {
				case v > best:
					second = best
					best = v
				case v > second:
					second = v
				}
			}
			if math.IsInf(best, -1) {
				out[qi][ii] = math.Inf(-1)
				continue
			}
			if agg == AggTop2 && !math.IsInf(second, -1) {
				out[qi][ii] = (best + second) / 2
			} else {
				out[qi][ii] = best
			}
		}
	}
	return out
}

// ZScoreRows стандартизует каждую строку оценок по конечным значениям —
// делает разные дескрипторы сопоставимыми (per-query standardisation).
// Строка без конечных значений остаётся целиком -Inf.
func ZScoreRows(s [][]float64) [][]float64 {
	out := make([][]float64, len(s))
	for i, row := range s {
		out[i] = make([]float64, len(row))
		var vals []float64
		for _, v := range row {
			if !math.IsInf(v, -1) && !math.IsNaN(v) {
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			for j := range out[i] {
				out[i][j] = math.Inf(-1)
			}
			continue
		}
		mean := 0.0
		for _, v := range vals {
			mean += v
		}
		mean /= float64(len(vals))
		sd := 1.0
		if len(vals) > 1 {
			var sq float64
			for _, v := range vals {
				d := v - mean
				sq += d * d
			}
			sd = math.Sqrt(sq / float64(len(vals)))
		}
		for j, v := range row {
			if math.IsInf(v, -1) || math.IsNaN(v) {
				out[i][j] = math.Inf(-1)
				continue
			}
			out[i][j] = (v - mean) / (sd + zScoreEpsilon)
		}
	}
	return out
}

// Fuse взвешенно суммирует z-нормированные матрицы оценок дескрипторов.
// names задаёт детерминированный порядок суммирования; вес 0 пропускается.
func Fuse(scoreMats map[string][][]float64, names []string, weights map[string]float64) [][]float64 {
	var total [][]float64
	for _, name := range names {
		w := weights[name]
		if w == 0 {
			continue
		}
		z := ZScoreRows(scoreMats[name])
		if total == nil {
			total = make([][]float64, len(z))
			for i := range z {
				total[i] = make([]float64, len(z[i]))
			}
		}
		for i := range z {
			for j := range z[i] {
				if !math.IsInf(z[i][j], -1) {
					total[i][j] += w * z[i][j]
				}
			}
		}
	}
	return total
}

// PairFeatures строит признаки пары (запрос, личность) для каждого
// дескриптора: [сырая оценка, z-оценка, зазор до лучшего]. Ровно тот набор,
// что в pair_features оригинала: логистическая регрессия кладёт разнородные
// близости на общую вероятностную шкалу (идея WildFusion в миниатюре).
// Возвращает Q x I x (3*len(names)).
func PairFeatures(scoreMats map[string][][]float64, names []string) [][][]float64 {
	q := 0
	if len(names) > 0 {
		q = len(scoreMats[names[0]])
	}
	out := make([][][]float64, q)
	for i := range out {
		out[i] = make([][]float64, len(scoreMats[names[0]][0]))
	}
	zs := make(map[string][][]float64, len(names))
	best := make(map[string][]float64, len(names))
	for _, name := range names {
		s := scoreMats[name]
		zs[name] = ZScoreRows(s)
		for i, row := range s {
			b := math.Inf(-1)
			for _, v := range row {
				if v > b {
					b = v
				}
			}
			if best[name] == nil {
				best[name] = make([]float64, len(s))
			}
			best[name][i] = b
		}
	}
	for i := 0; i < q; i++ {
		for j := range out[i] {
			feats := make([]float64, 0, 3*len(names))
			for _, name := range names {
				s := scoreMats[name][i][j]
				if math.IsInf(s, -1) {
					// исходно -Inf: сырая оценка 0, z нейтральный, зазор фиксированный
					feats = append(feats, 0, zFallback, gapFallback)
					continue
				}
				z := zs[name][i][j]
				if math.IsInf(z, -1) {
					z = zFallback
				}
				feats = append(feats, s, z, best[name][i]-s)
			}
			out[i][j] = feats
		}
	}
	return out
}

// Logistic — логистическая регрессия над pair-признаками:
// P(запрос — эта личность) = sigmoid(w * x + b).
//
// ИСТОЧНИК КОЭФФИЦИЕНТОВ: в zapovednik-public калибровщик обучается на месте
// (Calibrator.fit, sklearn LogisticRegression, C=1.0, class_weight=balanced)
// и в репозитории нет сериализованных коэффициентов — там они живут в
// артефактах deployments. Поэтому коэффициенты подаются снаружи
// (конфиг движка / FusionConfig), а не запечены константой: пока они не
// заданы, калибровка выключена и используется сырой фьюжн-скор.
type Logistic struct {
	Weights []float64
	Bias    float64
}

// Probability считает сигмоиду линейной комбинации признаков.
func (l *Logistic) Probability(features []float64) float64 {
	var z float64
	for i, w := range l.Weights {
		if i < len(features) {
			z += w * features[i]
		}
	}
	z += l.Bias
	return 1 / (1 + math.Exp(-z))
}

// Calibrate применяет калибровку к матрицам оценок: возвращает Q x I
// вероятностей; пары без конечных оценок первого дескриптора получают 0.
func (l *Logistic) Calibrate(scoreMats map[string][][]float64, names []string) [][]float64 {
	feats := PairFeatures(scoreMats, names)
	out := make([][]float64, len(feats))
	for i := range feats {
		out[i] = make([]float64, len(feats[i]))
		for j := range feats[i] {
			out[i][j] = l.Probability(feats[i][j])
		}
	}
	// пары, недоступные в первом дескрипторе, обнуляются (как в predict)
	first := scoreMats[names[0]]
	for i := range first {
		for j := range first[i] {
			if math.IsInf(first[i][j], -1) {
				out[i][j] = 0
			}
		}
	}
	return out
}

// OpenSetThreshold выбирает порог по top-1 вероятности: ниже порога —
// «возможно новая/неизвестная особь». Порт open_set_threshold: порог берётся
// такой, что доля targetKnownRecall известных особей остаётся выше порога.
func OpenSetThreshold(knownProbabilities []float64, targetKnownRecall float64) float64 {
	if len(knownProbabilities) == 0 {
		return 0.5
	}
	sorted := append([]float64(nil), knownProbabilities...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	idx := int((1 - targetKnownRecall) * float64(len(sorted)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// IsNewSuspected — открытый набор: лучшая откалиброванная вероятность ниже
// порога означает «возможно новая особь».
func IsNewSuspected(bestProbability, threshold float64) bool {
	return bestProbability < threshold
}

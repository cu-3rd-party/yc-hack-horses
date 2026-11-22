package reid

// Определение ракурса (левый/правый профиль) — порт семантики
// ml/reid/viewpoint.py проекта zapovednik-public. Идея: для кропа x и его
// зеркала flip(x) разность d = E(x) - E(flip(x)) меняет знак при отражении,
// поэтому главная ось вариаций {d} (симметризованная добавлением -d)
// разделяет два профиля без разметки. Фронтальные/неоднозначные ракурсы
// проецируются около нуля и помечаются как unknown (сравниваем со всеми).
// Реализация написана с нуля на Go, см. NOTICE.md.

import (
	"math"
	"math/rand"
)

// sideAxisIterations — число шагов степенной итерации для оценки оси
// (в оригинале 50 итераций на ковариации, sklearn не нужен).
const sideAxisIterations = 50

// FitSideAxis возвращает единичную ось ракурса по разностным векторам
// d = E(x) - E(flip(x)). Степенная итерация на ковариации симметризованного
// набора {d, -d}; детерминирована благодаря фиксированному зерну.
func FitSideAxis(d [][]float64) []float64 {
	if len(d) == 0 {
		return nil
	}
	dim := len(d[0])
	// симметризация {d, -d} даёт нулевое среднее по построению —
	// центрировка не меняет ковариацию
	rng := rand.New(rand.NewSource(0))
	v := make([]float64, dim)
	for j := range v {
		v[j] = rng.NormFloat64()
	}
	for range sideAxisIterations {
		next := make([]float64, dim)
		for _, row := range d {
			var fwd, bwd float64
			for j, x := range row {
				fwd += x * v[j]
				bwd -= x * v[j]
			}
			for j, x := range row {
				next[j] += (fwd - bwd) * x
			}
		}
		var norm float64
		for _, x := range next {
			norm += x * x
		}
		norm = math.Sqrt(norm) + 1e-12
		for j := range next {
			v[j] = next[j] / norm
		}
	}
	return v
}

// Projection — проекция разностного вектора на ось ракурса.
func Projection(d, axis []float64) float64 {
	var s float64
	for j, a := range axis {
		if j < len(d) {
			s += d[j] * a
		}
	}
	return s
}

// AssignSide классифицирует проекции в 'A'/'B'/'unknown': доля
// unknownQuantile самых маленьких по модулю проекций считается неоднозначной
// (в оригинале 0.15).
func AssignSide(projections []float64, unknownQuantile float64) []string {
	abs := make([]float64, len(projections))
	for i, s := range projections {
		if s < 0 {
			s = -s
		}
		abs[i] = s
	}
	thr := linearQuantile(abs, unknownQuantile)
	sides := make([]string, len(projections))
	for i, s := range projections {
		switch {
		case s > thr:
			sides[i] = "A"
		case s < -thr:
			sides[i] = "B"
		default:
			sides[i] = "unknown"
		}
	}
	return sides
}

// SideOf — ракурс одного запроса по его проекции и порогу.
func SideOf(projection float64, threshold float64) string {
	switch {
	case projection > threshold:
		return "A"
	case projection < -threshold:
		return "B"
	default:
		return "unknown"
	}
}

// SameSideAllowed — разрешено ли сравнение: тот же ракурс, либо любой из
// двоих unknown (порт same_side_mask).
func SameSideAllowed(querySide, gallerySide string) bool {
	return querySide == gallerySide ||
		querySide == "unknown" || querySide == "" ||
		gallerySide == "unknown" || gallerySide == ""
}

// linearQuantile — линейно интерполированный квантиль (как np.quantile
// по умолчанию); имя отличается от quantile в physical.go с иной семантикой.
func linearQuantile(sortedAny []float64, q float64) float64 {
	if len(sortedAny) == 0 {
		return 0
	}
	s := append([]float64(nil), sortedAny...)
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	if q <= 0 {
		return s[0]
	}
	if q >= 1 {
		return s[len(s)-1]
	}
	pos := q * float64(len(s)-1)
	lo := int(pos)
	hi := lo + 1
	if hi >= len(s) {
		return s[lo]
	}
	frac := pos - float64(lo)
	return s[lo] + frac*(s[hi]-s[lo])
}

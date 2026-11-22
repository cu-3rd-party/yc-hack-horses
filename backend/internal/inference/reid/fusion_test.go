package reid

// Числовые эталоны для порта скоринга сверены с numpy-реализацией
// ml/reid/matching.py zapovednik-public на фиксированных входах.

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// Тестовая матрица близостей: 2 запроса, 4 кропа галереи (A,A,B,B),
// в строке 1 кроп 3 (B) запрещён.
var fusionSim = [][]float64{
	{0.9, 0.8, 0.3, 0.1},
	{0.4, 0.2, 0.7, 0.6},
}

var fusionAllowed = [][]bool{
	{true, true, true, true},
	{true, true, true, false},
}

func closeEnough(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

func requireRow(t *testing.T, got, want []float64) {
	t.Helper()
	require.Len(t, got, len(want))
	for i := range want {
		if math.IsInf(want[i], -1) {
			require.True(t, math.IsInf(got[i], -1), "i=%d: ожидалась -Inf, получено %v", i, got[i])
			continue
		}
		require.True(t, closeEnough(got[i], want[i]), "i=%d: %v != %v", i, got[i], want[i])
	}
}

func TestIdentityScoresMatchesOriginalSemantics(t *testing.T) {
	galIDs := []string{"A", "A", "B", "B"}
	idents := []string{"A", "B"}
	got := IdentityScores(fusionSim, galIDs, idents, fusionAllowed, AggMax)
	// numpy: [[0.9, 0.3], [0.4, 0.7]] (B во второй строке ограничена маской)
	requireRow(t, got[0], []float64{0.9, 0.3})
	requireRow(t, got[1], []float64{0.4, 0.7})

	top2 := IdentityScores(fusionSim, galIDs, idents, fusionAllowed, AggTop2)
	// numpy: [[0.85, 0.2], [0.3, 0.7]]: у B в строке 1 один разрешённый кроп — берётся max
	requireRow(t, top2[0], []float64{0.85, 0.2})
	requireRow(t, top2[1], []float64{0.3, 0.7})
}

func TestZScoreRowsMatchesOriginalSemantics(t *testing.T) {
	galIDs := []string{"A", "A", "B", "B"}
	idents := []string{"A", "B"}
	s := IdentityScores(fusionSim, galIDs, idents, fusionAllowed, AggMax)
	z := ZScoreRows(s)
	// numpy: [[0.999997, -0.999997], [-0.999993, 0.999993]]
	requireRow(t, z[0], []float64{0.9999966667, -0.9999966667})
	requireRow(t, z[1], []float64{-0.9999933334, 0.9999933334})

	// строка из одного конечного значения: sd=1, z=0
	single := ZScoreRows([][]float64{{0.7}})
	requireRow(t, single[0], []float64{0})

	// строка без конечных значений остаётся -Inf
	empty := ZScoreRows([][]float64{{math.Inf(-1), math.Inf(-1)}})
	require.True(t, math.IsInf(empty[0][0], -1))
}

func TestFuseMatchesOriginalSemantics(t *testing.T) {
	galIDs := []string{"A", "A", "B", "B"}
	idents := []string{"A", "B"}
	s := IdentityScores(fusionSim, galIDs, idents, fusionAllowed, AggMax)
	fused := Fuse(map[string][][]float64{"v": s}, []string{"v"}, map[string]float64{"v": 2})
	z := ZScoreRows(s)
	requireRow(t, fused[0], []float64{2 * z[0][0], 2 * z[0][1]})

	// нулевой вес дескриптора пропускается: результат не определён (nil)
	require.Nil(t, Fuse(map[string][][]float64{"v": s}, []string{"v"}, map[string]float64{"v": 0}))
}

func TestPairFeaturesMatchesOriginalSemantics(t *testing.T) {
	s := [][]float64{{0.9, 0.3}, {0.2, 0.8}}
	mats := map[string][][]float64{"v": s}
	feats := PairFeatures(mats, []string{"v"})
	// numpy: [[[0.9, 0.9999966667, 0.0], [0.3, -0.9999966667, 0.6]],
	//         [[0.2, -0.9999966667, 0.6], [0.8, 0.9999966667, 0.0]]]
	requireRow(t, feats[0][0], []float64{0.9, 0.9999966667, 0})
	requireRow(t, feats[0][1], []float64{0.3, -0.9999966667, 0.6000000000000001})
	requireRow(t, feats[1][0], []float64{0.2, -0.9999966667, 0.6000000000000001})
	requireRow(t, feats[1][1], []float64{0.8, 0.9999966667, 0})

	// недоступная пара (-Inf): нейтральные подстановки [0, -5, 2]
	mats["v"][0][1] = math.Inf(-1)
	feats = PairFeatures(mats, []string{"v"})
	requireRow(t, feats[0][1], []float64{0, zFallback, gapFallback})
}

func TestLogisticMatchesOriginalSemantics(t *testing.T) {
	s := [][]float64{{0.9, 0.3}, {0.2, 0.8}}
	mats := map[string][][]float64{"v": s}
	lr := &Logistic{Weights: []float64{1.0, 0.5, -0.2}, Bias: 0.1}
	p := lr.Calibrate(mats, []string{"v"})
	// numpy: [[0.8175742276, 0.4452211766], [0.4206761540, 0.8021836241]]
	require.True(t, closeEnough(p[0][0], 0.8175742276), "p[0][0]=%v", p[0][0])
	require.True(t, closeEnough(p[0][1], 0.4452211766), "p[0][1]=%v", p[0][1])
	require.True(t, closeEnough(p[1][0], 0.4206761540), "p[1][0]=%v", p[1][0])
	require.True(t, closeEnough(p[1][1], 0.8021836241), "p[1][1]=%v", p[1][1])

	// недоступная пара обнуляется
	mats["v"][0][1] = math.Inf(-1)
	p = lr.Calibrate(mats, []string{"v"})
	require.Equal(t, 0.0, p[0][1])
}

func TestOpenSetThresholdMatchesOriginalSemantics(t *testing.T) {
	// 20 известных: порог при recall 0.95 — элемент с индексом int(0.05*20)=1
	known := make([]float64, 20)
	for i := range known {
		known[i] = float64(i) / 20
	}
	thr := OpenSetThreshold(known, 0.95)
	require.True(t, closeEnough(thr, 0.05), "thr=%v", thr)

	// пустой список — нейтральный порог 0.5
	require.Equal(t, 0.5, OpenSetThreshold(nil, 0.95))

	// IsNewSuspected: ниже порога — возможно новая особь
	require.True(t, IsNewSuspected(0.04, thr))
	require.False(t, IsNewSuspected(0.9, thr))
}

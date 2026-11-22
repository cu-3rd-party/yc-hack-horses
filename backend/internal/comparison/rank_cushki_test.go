package comparison

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
)

// RankCushki — ранжирование CUshki v6: косинус (agg=max), калибровка головой
// fusion_config.json при полном покрытии классами, кросс-версии отсечены.
func TestRankCushkiCosineOrder(t *testing.T) {
	refs := []Reference{
		{HorseID: "a", Name: "Азарм", PhotoID: "p1", Descriptor: Descriptor{Version: reid.CushkiVersion, Vector: []float64{1, 0, 0, 0}}},
		{HorseID: "b", Name: "Беркут", PhotoID: "p2", Descriptor: Descriptor{Version: reid.CushkiVersion, Vector: []float64{0, 1, 0, 0}}},
	}
	query := Descriptor{Version: reid.CushkiVersion, Vector: []float64{1, 0, 0, 0}}
	out := RankCushki(nil, query, refs, 5)
	require.Len(t, out, 2)
	require.Equal(t, "a", out[0].HorseID)
	require.InDelta(t, 1, out[0].Similarity, 1e-9)
	require.Equal(t, 0.0, out[0].Probability) // голова не задана

	// Кросс-версия не ранжируется.
	mega := Descriptor{Version: reid.BoxVersion, Vector: make([]float64, reid.Dimension)}
	require.Empty(t, RankCushki(nil, mega, refs, 5))
	// Эталоны другой cushki-версии отсечены фильтром версии.
	refs2 := []Reference{{HorseID: "a", Name: "Азарм", PhotoID: "p1", Descriptor: Descriptor{Version: reid.CushkiCentralVersion, Vector: []float64{1, 0, 0, 0}}}}
	require.Empty(t, RankCushki(nil, query, refs2, 5))
}

func TestRankCushkiHeadCalibration(t *testing.T) {
	head := &reid.CushkiConfig{
		Logistic:     reid.CushkiLogistic{Weights: [][]float64{{1, 0, 0, 0}, {0, 1, 0, 0}}, Bias: []float64{0, 0}},
		Classes:      []string{"a", "b"},
		NewThreshold: 0.3,
		FeatureLayout: reid.CushkiFeatureLayout{
			Dim: 4, Blocks: []string{"s2-raw", "s2-masked", "s3-raw", "s3-masked"}, BlockDims: 1,
		},
	}
	refs := []Reference{
		{HorseID: "a", Name: "Азарм", PhotoID: "p1", Descriptor: Descriptor{Version: reid.CushkiVersion, Vector: []float64{1, 0, 0, 0}}},
		{HorseID: "b", Name: "Беркут", PhotoID: "p2", Descriptor: Descriptor{Version: reid.CushkiVersion, Vector: []float64{0, 1, 0, 0}}},
	}
	// Признак ближе к классу "b": голова перекалибровывает порядок.
	query := Descriptor{Version: reid.CushkiVersion, Vector: []float64{0, 2, 0, 0}}
	out := RankCushki(head, query, refs, 5)
	require.Len(t, out, 2)
	require.Equal(t, "b", out[0].HorseID)
	require.Greater(t, out[0].Probability, out[1].Probability)
	require.InDelta(t, 1, out[0].Probability+out[1].Probability, 1e-9)

	// Личность вне классов головы — порядок остаётся косинусным, вероятность 0.
	refs3 := []Reference{refs[0], {HorseID: "x", Name: "Незнакомец", PhotoID: "p3", Descriptor: Descriptor{Version: reid.CushkiVersion, Vector: []float64{0, 1, 0, 0}}}}
	out = RankCushki(head, query, refs3, 5)
	require.Equal(t, 0.0, out[0].Probability)
	require.Equal(t, "x", out[0].HorseID)
}

func TestCushkiIsNewSuspectedThreshold(t *testing.T) {
	cfg := &reid.CushkiConfig{
		Logistic:     reid.CushkiLogistic{Weights: [][]float64{{1, 0}, {0, 1}}, Bias: []float64{0, 0}},
		Classes:      []string{"a", "b"},
		NewThreshold: 0.3,
	}
	// P=0.5 > 0.3 — известная особь.
	require.False(t, reid.CushkiIsNewSuspected(cfg, []float64{0, 0}))
	// Явный перекос в сторону одного класса при большом |x| — уверенность растёт.
	require.False(t, reid.CushkiIsNewSuspected(cfg, []float64{10, 0}))
}

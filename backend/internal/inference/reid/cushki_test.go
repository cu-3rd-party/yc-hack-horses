package reid

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

// Тесты CUshki v6: парсинг fusion_config.json (фикстура — уменьшенная
// копия реального файла экспорта весов), динамическая размерность эмбеддингов
// и поведение без конфига/файла.

func loadCushkiFixture(t *testing.T) *CushkiConfig {
	t.Helper()
	cfg, err := LoadCushkiConfig("testdata/cushki_fusion_config.json")
	require.NoError(t, err)
	return cfg
}

func TestLoadCushkiConfig(t *testing.T) {
	cfg := loadCushkiFixture(t)
	require.Equal(t, []string{"Азарм", "Беркут"}, cfg.Classes)
	require.Equal(t, 0.3, cfg.NewThreshold)
	require.Len(t, cfg.Logistic.Weights, 2)
	require.Equal(t, 4, cfg.FeatureDim())
	require.Equal(t, 2, cfg.Members())
	require.Equal(t, 2, cfg.MemberDim()) // 2*block_dims = 2*1
	require.Len(t, cfg.SideAxis.Axis, cfg.SideAxis.Dim)
}

func TestLoadCushkiConfigMissingFile(t *testing.T) {
	_, err := LoadCushkiConfig("testdata/no_such_fusion_config.json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cushki config")
}

func TestCushkiConfigValidate(t *testing.T) {
	cfg := loadCushkiFixture(t)
	require.NoError(t, cfg.Validate())

	bad := *cfg
	bad.Logistic.Weights = bad.Logistic.Weights[:1]
	require.Error(t, bad.Validate())

	bad2 := *cfg
	bad2.NewThreshold = 0
	require.NoError(t, bad2.Validate()) // порог не обязан присутствовать
}

func TestCushkiFeatures(t *testing.T) {
	cfg := loadCushkiFixture(t)
	raw := []float32{1, 2}
	masked := []float32{3, 4}
	x, err := cfg.Features(raw, masked)
	require.NoError(t, err)
	require.Len(t, x, cfg.FeatureDim())
	// [raw, masked]/sqrt(2) на члена, члены конкатенированы /sqrt(2):
	// порядок блоков s2-raw, s2-masked, s3-raw, s3-masked.
	scale := math.Sqrt2 * math.Sqrt2
	require.InDelta(t, 1/scale, x[0], 1e-12)
	require.InDelta(t, 3/scale, x[1], 1e-12)
	require.InDelta(t, 2/scale, x[2], 1e-12)
	require.InDelta(t, 4/scale, x[3], 1e-12)

	_, err = cfg.Features([]float32{1}, masked)
	require.Error(t, err)
}

func TestCushkiProbabilitiesAndOpenSet(t *testing.T) {
	cfg := loadCushkiFixture(t)
	x := make([]float64, cfg.FeatureDim())
	p := cfg.Probabilities(x)
	require.NotNil(t, p)
	require.Len(t, p, 2)
	var sum float64
	for _, v := range p {
		sum += v
	}
	require.InDelta(t, 1, sum, 1e-9)

	// Вероятности на вероятностной шкале: open-set 0.3 отсекает низкую
	// уверенность лучшего класса.
	best := math.Max(p[0], p[1])
	require.Equal(t, best < cfg.NewThreshold, cfg.IsNewSuspected(best))
	// Нулевой признак: вероятности = softmax(bias); убеждаемся, что порог
	// работает на границе.
	require.Equal(t, true, cfg.IsNewSuspected(0.299))
	require.Equal(t, false, cfg.IsNewSuspected(0.3))

	// Несовпадающая размерность — nil, голова неприменима.
	require.Nil(t, cfg.Probabilities(make([]float64, 3)))
	// Без головы «новая особь» не объявляется.
	require.False(t, CushkiIsNewSuspected(nil, x))
}

func TestCushkiCalibratedScores(t *testing.T) {
	cfg := loadCushkiFixture(t)
	x := make([]float64, cfg.FeatureDim())
	scores, all := cfg.CalibratedScores([]string{"Азарм", "Беркут"}, x)
	require.True(t, all)
	require.Len(t, scores, 2)
	require.InDelta(t, 1, scores[0]+scores[1], 1e-9)

	// Личность вне классов головы: NaN, allCalibrated=false.
	scores, all = cfg.CalibratedScores([]string{"Азарм", "Незнакомец"}, x)
	require.False(t, all)
	require.False(t, math.IsNaN(scores[0]))
	require.True(t, math.IsNaN(scores[1]))
}

func TestCushkiDynamicDimension(t *testing.T) {
	// cushki-версии несут размерность модели (2048 у экспорта), не константу:
	// валидна любая положительная длина, megadescriptor остаётся 1536.
	require.True(t, ValidVector(CushkiVersion, 2048))
	require.True(t, ValidVector(CushkiCentralVersion, 2048))
	require.True(t, ValidVector(CushkiVersion, 4096)) // другой состав ансамбля
	require.False(t, ValidVector(CushkiVersion, 0))
	require.True(t, ValidVector(Version, Dimension))
	require.False(t, ValidVector(Version, 2048))
	require.True(t, ValidVector(BoxVersion, Dimension))
	require.True(t, ValidVector(legacyVersion, Dimension+10))
	require.False(t, ValidVector(legacyVersion, Dimension))

	e := inference.EmbeddingResult{ModelVersion: CushkiVersion, Vector: make([]float32, 2048)}
	for i := range e.Vector {
		e.Vector[i] = 0.01
	}
	require.NoError(t, Validate(e))

	other := inference.EmbeddingResult{ModelVersion: Version, Vector: make([]float32, Dimension)}
	other.Vector[0] = 1
	// Кросс megadescriptor/cushki отвергается.
	_, err := Similarity(e, other)
	require.Error(t, err)
	// Разные длины внутри cushki отвергаются.
	short := inference.EmbeddingResult{ModelVersion: CushkiVersion, Vector: make([]float32, 1024)}
	short.Vector[0] = 1
	_, err = Similarity(e, short)
	require.Error(t, err)
	// Одинаковые cushki-векторы сравнимы.
	same := inference.EmbeddingResult{ModelVersion: CushkiVersion, Vector: append([]float32(nil), e.Vector...)}
	score, err := Similarity(e, same)
	require.NoError(t, err)
	require.InDelta(t, 1, score, 1e-6)
}

func TestCushkiRankGallery(t *testing.T) {
	cfg := loadCushkiFixture(t)
	query := []float64{1, 0, 0, 0}
	a := []float64{1, 0, 0, 0}
	b := []float64{0, 1, 0, 0}
	gallery := [][]float64{a, b, a}
	ids := []string{"Азарм", "Азарм", "Беркут"}
	scores, err := CushkiRankGallery(cfg, query, gallery, ids, []string{"Азарм", "Беркут"})
	require.NoError(t, err)
	require.Equal(t, 2, scores[0].Hits)
	require.Equal(t, 1, scores[1].Hits)
	require.False(t, math.IsNaN(scores[0].Probability))
	require.False(t, math.IsNaN(scores[1].Probability))

	// Косинус без головы, порядок по косинусу.
	scores, err = CushkiRankGallery(nil, b, gallery, ids, []string{"Азарм", "Беркут"})
	require.NoError(t, err)
	require.True(t, math.IsNaN(scores[0].Probability))
	order := CushkiOrder(scores)
	// b ближе к кропу "Азарм" (второй кроп b)? b·b=1 у кропа b (id Азарм).
	require.Equal(t, 0, order[0])
}

func TestCushkiQueryFeature(t *testing.T) {
	cfg := loadCushkiFixture(t)
	raw := inference.EmbeddingResult{ModelVersion: CushkiVersion, Vector: []float32{1, 2}}
	masked := inference.EmbeddingResult{ModelVersion: CushkiVersion, Vector: []float32{3, 4}}
	x, err := CushkiQueryFeature(cfg, raw, masked)
	require.NoError(t, err)
	require.Len(t, x, cfg.FeatureDim())

	// Кросс-версия отвергается.
	bad := inference.EmbeddingResult{ModelVersion: Version, Vector: []float32{1, 2}}
	_, err = CushkiQueryFeature(cfg, raw, bad)
	require.Error(t, err)

	// Без головы признак не нужен.
	x, err = CushkiQueryFeature(nil, raw, masked)
	require.NoError(t, err)
	require.Nil(t, x)
}

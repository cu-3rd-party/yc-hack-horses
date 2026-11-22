// cushkiconfig.go — конфиг CUshki v6 (файл fusion_config.json из экспорта
// весов) и матричная логистическая голова над сшитым признаком ансамбля.
//
// Это ОТДЕЛЬНЫЙ формат от reid.FusionConfig (zapovednik head.npz): у CUshki
// logistic.weights — МАТРИЦА C×D (мультиномиальная логистическая регрессия
// над признаковым вектором x: z_i = w_i·x + b_i, softmax по классам), плюс
// классы, feature_layout (как собран признак из членов ансамбля) и side_axis
// (ось ракурса). Смешивать форматы в одном типе было бы грязно — семантика
// весов разная (вектор над pair-признаками vs матрица над признаками), поэтому
// заведён отдельный тип CushkiConfig. См. NOTICE.md.
//
// Конфиг опционален: без него cushki-путь работает чистым косинусом
// (дефолтное поведение при отсутствии файлов — прежнее, megadescriptor).
package reid

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// CushkiFeatureLayout описывает сборку признака из членов ансамбля:
// признак = concat по членам [raw, masked]/sqrt(2), затем /sqrt(M);
// block_dims — размерность одного блока (CLS+patch-mean одного члена).
type CushkiFeatureLayout struct {
	Dim       int      `json:"dim"`
	Blocks    []string `json:"blocks"`
	BlockDims int      `json:"block_dims"`
	Note      string   `json:"note,omitempty"`
}

// CushkiSideAxis — безразмерная ось «левый/правый профиль» (порт
// reid/viewpoint.py fit_axis): s = x·axis; s > d — сторона A, s < -d — B,
// |s| < d — unknown. Порог d вычисляется по квантили 0.15 на этапе обучения
// и в конфиг не входит; здесь ось хранится для side-aware фильтра.
type CushkiSideAxis struct {
	Dim       int       `json:"dim"`
	Axis      []float64 `json:"axis"`
	Semantics string    `json:"semantics,omitempty"`
}

type CushkiLogistic struct {
	// Weights — матрица C×D: по строке на класс.
	Weights [][]float64 `json:"weights"`
	// Bias — свободные члены, по одному на класс.
	Bias []float64 `json:"bias"`
}

// CushkiConfig — содержимое fusion_config.json CUshki v6.
type CushkiConfig struct {
	Logistic      CushkiLogistic      `json:"logistic"`
	NewThreshold  float64             `json:"new_threshold"`
	Classes       []string            `json:"classes"`
	FeatureLayout CushkiFeatureLayout `json:"feature_layout"`
	SideAxis      CushkiSideAxis      `json:"side_axis"`
}

// LoadCushkiConfig читает и валидирует fusion_config.json. Отсутствие файла —
// ошибка: путь задан явно, тихий fallback скрыл бы опечатку в конфиге.
func LoadCushkiConfig(path string) (*CushkiConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reid: cushki config %s: %w", path, err)
	}
	var cfg CushkiConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("reid: cushki config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("reid: cushki config %s: %w", path, err)
	}
	return &cfg, nil
}

// Validate проверяет согласованность размеров: строки матрицы = классы,
// длина строки = dim признака, блоки раскладываются ровно в dim, ось ракурса
// согласована со своей размерностью.
func (c *CushkiConfig) Validate() error {
	if len(c.Classes) == 0 {
		return fmt.Errorf("no classes")
	}
	if len(c.Logistic.Weights) != len(c.Classes) {
		return fmt.Errorf("logistic weights: %d rows for %d classes", len(c.Logistic.Weights), len(c.Classes))
	}
	if len(c.Logistic.Bias) != len(c.Classes) {
		return fmt.Errorf("logistic bias: %d values for %d classes", len(c.Logistic.Bias), len(c.Classes))
	}
	d := len(c.Logistic.Weights[0])
	if d == 0 {
		return fmt.Errorf("logistic weights: empty row")
	}
	for i, row := range c.Logistic.Weights {
		if len(row) != d {
			return fmt.Errorf("logistic weights: row %d has %d values, expected %d", i, len(row), d)
		}
		for _, w := range row {
			if math.IsNaN(w) || math.IsInf(w, 0) {
				return fmt.Errorf("logistic weights: row %d has a non-finite value", i)
			}
		}
	}
	if c.FeatureLayout.Dim != d {
		return fmt.Errorf("feature_layout.dim %d != logistic width %d", c.FeatureLayout.Dim, d)
	}
	if len(c.FeatureLayout.Blocks) == 0 || c.FeatureLayout.BlockDims <= 0 ||
		len(c.FeatureLayout.Blocks)*c.FeatureLayout.BlockDims != c.FeatureLayout.Dim {
		return fmt.Errorf("feature_layout: %d blocks x %d dims != dim %d", len(c.FeatureLayout.Blocks), c.FeatureLayout.BlockDims, c.FeatureLayout.Dim)
	}
	if len(c.SideAxis.Axis) != c.SideAxis.Dim {
		return fmt.Errorf("side_axis: %d values for dim %d", len(c.SideAxis.Axis), c.SideAxis.Dim)
	}
	return nil
}

// FeatureDim — размерность признака, на которой обучена голова.
func (c *CushkiConfig) FeatureDim() int { return len(c.Logistic.Weights[0]) }

// MemberDim — размерность эмбеддинга одного члена ансамбля: [raw, masked]
// одного члена занимает 2*block_dims. Членам ансамбля соответствует столько
// моделей, сколько блоков в feature_layout, делённое пополам.
func (c *CushkiConfig) MemberDim() int { return 2 * c.FeatureLayout.BlockDims }

// Members — число членов ансамбля (блоков вдвое больше: raw+masked).
func (c *CushkiConfig) Members() int { return len(c.FeatureLayout.Blocks) / 2 }

// Features сшивает признак запроса из эмбеддингов членов ансамбля: для
// каждого члена concat(raw, masked)/sqrt(2), затем конкатенация по членам
// /sqrt(M) — ровно та сборка, которой обучалась голова (06_build_bundle.py).
// raw и masked — по MemberDim на члена, в порядке членов ансамбля.
func (c *CushkiConfig) Features(raw, masked []float32) ([]float64, error) {
	m := c.Members()
	if len(raw) != m*c.FeatureLayout.BlockDims || len(masked) != m*c.FeatureLayout.BlockDims {
		return nil, fmt.Errorf("reid: cushki features: expected %d values per variant for %d members, got %d/%d",
			m*c.FeatureLayout.BlockDims, m, len(raw), len(masked))
	}
	x := make([]float64, 0, c.FeatureDim())
	scale := math.Sqrt(2) * math.Sqrt(float64(m))
	for b := 0; b < m; b++ {
		off := b * c.FeatureLayout.BlockDims
		for i := off; i < off+c.FeatureLayout.BlockDims; i++ {
			x = append(x, float64(raw[i])/scale)
		}
		for i := off; i < off+c.FeatureLayout.BlockDims; i++ {
			x = append(x, float64(masked[i])/scale)
		}
	}
	return x, nil
}

// Probabilities считает softmax(z_i) для z_i = w_i·x + b_i — те же вероятности,
// что в model_server.identify (temperature = 1). Возвращает nil, если размерность
// признака не совпадает с головой (голова неприменима — скоринг остаётся косинусным).
func (c *CushkiConfig) Probabilities(x []float64) []float64 {
	if len(x) != c.FeatureDim() {
		return nil
	}
	p := make([]float64, len(c.Classes))
	max := math.Inf(-1)
	for i, w := range c.Logistic.Weights {
		z := c.Logistic.Bias[i]
		for j, v := range w {
			z += v * x[j]
		}
		p[i] = z
		if z > max {
			max = z
		}
	}
	var sum float64
	for i, z := range p {
		p[i] = math.Exp(z - max)
		sum += p[i]
	}
	if sum == 0 || math.IsNaN(sum) || math.IsInf(sum, 0) {
		return nil
	}
	for i := range p {
		p[i] /= sum
	}
	return p
}

// CalibratedScores калибрует оценки личностей головой: личность, имя которой
// совпадает с классом обучения, получает вероятность P(класс | признак
// запроса). Личности вне списка классов получают NaN — они не калибруются
// головой (голова — классификатор над известными ей особями).
// allCalibrated=true, если каждая личность галереи покрыта классами.
func (c *CushkiConfig) CalibratedScores(identities []string, x []float64) (scores []float64, allCalibrated bool) {
	p := c.Probabilities(x)
	scores = make([]float64, len(identities))
	index := make(map[string]int, len(c.Classes))
	for i, name := range c.Classes {
		index[name] = i
	}
	allCalibrated = p != nil
	for i, id := range identities {
		if j, ok := index[id]; ok && p != nil {
			scores[i] = p[j]
		} else {
			scores[i] = math.NaN()
			allCalibrated = false
		}
	}
	return scores, allCalibrated
}

// IsNewSuspected — открытый набор CUshki: вероятность лучшего класса ниже
// new_threshold (0.3 в релизном конфиге) означает «возможно новая особь».
func (c *CushkiConfig) IsNewSuspected(bestProbability float64) bool {
	return bestProbability < c.NewThreshold
}

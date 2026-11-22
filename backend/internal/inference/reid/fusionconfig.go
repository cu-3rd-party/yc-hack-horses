// fusionconfig.go — внешние коэффициенты скоринга ReID (порт артефакта
// bundle zapovednik-public: head.npz + weights фьюжна + open-set порог).
// Файл JSON опционален: без него скоринг идёт сырым фьюжн-скором —
// дефолтное поведение движка не меняется (безопасный деплой).
package reid

import (
	"encoding/json"
	"fmt"
	"os"
)

// FusionConfig — коэффициенты, обученные CUshki (head.npz ансамбля) и
// подаваемые снаружи: калибровка логрегрессией над pair-признаками, веса
// дескрипторов во фьюжне и порог «возможно новая особь».
type FusionConfig struct {
	// Logistic — коэффициенты логистической калибровки (head.npz).
	Logistic Logistic `json:"logistic"`
	// DescriptorWeights — вес каждого дескриптора во фьюжне (имя -> вес).
	// Пусто — равные веса.
	DescriptorWeights map[string]float64 `json:"descriptor_weights,omitempty"`
	// NewThreshold — порог open-set: top-1 вероятность ниже порога означает
	// «возможно новая особь» (isNewSuspected). 0 — порог не применяется.
	NewThreshold float64 `json:"new_threshold,omitempty"`
}

// LoadFusionConfig читает JSON-файл коэффициентов. Отсутствие файла — ошибка:
// путь задан явно, тихий fallback скрыл бы опечатку в конфиге.
func LoadFusionConfig(path string) (*FusionConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reid: fusion config %s: %w", path, err)
	}
	var fc FusionConfig
	if err := json.Unmarshal(raw, &fc); err != nil {
		return nil, fmt.Errorf("reid: fusion config %s: %w", path, err)
	}
	return &fc, nil
}

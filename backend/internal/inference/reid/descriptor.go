package reid

import (
	"fmt"
	"math"
	"strings"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
)

const (
	Version               = "megadescriptor-l384-silhouette-v1"
	publishedHeadsVersion = "megadescriptor-l384-silhouette-v1-h17766f17"
	BoxVersion            = "megadescriptor-l384-visual-v1"
	// Dimension — размерность мегадескрипторного пространства. Константа
	// остаётся привязанной к megadescriptor-пути: cushki-эмбеддинги несут
	// свою размерность, известную из модели (см. IsCushkiVersion).
	Dimension     = 1536
	legacyVersion = "megadescriptor-l384-pascal-parts-physical-v1"
)

// cushkiVersionPrefix — префикс версий эмбеддингов CUshki v6 (файнтюн
// DINOv2-L, ONNX-экспорт). Размерность вектора динамическая: она определяется
// моделью (2048 = concat(CLS, patch-mean) для одного члена ансамбля) и
// валидируется по фактической длине вектора, а не константой.
const cushkiVersionPrefix = "cushki-"

// CushkiVersion — версия эмбеддинга CUshki v6 из полного сшитого признака
// ансамбля [raw, masked] по всем членам (то самое пространство, в котором
// обучена логистическая голова fusion_config.json).
const CushkiVersion = "cushki-dinov2l-ft-448-v1"

// CushkiCentralVersion — версия с fallback-вариантом masked (центральный
// кроп вместо маски): держится в отдельном пространстве поиска, как и у
// megadescriptor-пути.
const CushkiCentralVersion = "cushki-dinov2l-ft-448-central-v1"

// IsCushkiVersion различает пространства CUshki и megadescriptor: валидация
// и сравнение отвергают кросс-версии (разные модели = несравнимые векторы,
// даже при совпадении формальной длины).
func IsCushkiVersion(version string) bool {
	return strings.HasPrefix(version, cushkiVersionPrefix)
}

// ValidVector сообщает, допустима ли длина вектора для версии эмбеддинга.
// megadescriptor-версии привязаны к Dimension; cushki-версии динамические
// (размерность задаёт модель), требуется лишь положительная длина.
func ValidVector(version string, n int) bool {
	switch {
	case n <= 0:
		return false
	case IsCushkiVersion(version):
		return true
	case version == legacyVersion:
		return n == Dimension+10
	default:
		return n == Dimension
	}
}

func visualEmbedding(values []float32, version string) (inference.EmbeddingResult, error) {
	if !ValidVector(version, len(values)) {
		return inference.EmbeddingResult{}, fmt.Errorf("reid: unexpected vector length %d for version %s", len(values), version)
	}
	var squared float64
	for _, x := range values {
		squared += float64(x) * float64(x)
	}
	if squared == 0 || math.IsNaN(squared) || math.IsInf(squared, 0) {
		return inference.EmbeddingResult{}, fmt.Errorf("reid: invalid visual vector")
	}
	norm := math.Sqrt(squared)
	for i, x := range values {
		values[i] = float32(float64(x) / norm)
	}
	return inference.EmbeddingResult{ModelVersion: version, Vector: values}, nil
}

// Old exports store the same visual block with a fixed scale, followed by ten trait coordinates.
func squaredVisualNorm(e inference.EmbeddingResult) (float64, error) {
	switch {
	case IsCushkiVersion(e.ModelVersion):
		if !ValidVector(e.ModelVersion, len(e.Vector)) {
			return 0, fmt.Errorf("reid: incompatible embedding version or dimensions")
		}
		var sum float64
		for _, x := range e.Vector {
			sum += float64(x) * float64(x)
		}
		if sum == 0 || math.IsNaN(sum) || math.IsInf(sum, 0) {
			return 0, fmt.Errorf("reid: invalid visual vector")
		}
		return sum, nil
	case (IsSilhouette(e.ModelVersion) || e.ModelVersion == BoxVersion) && len(e.Vector) == Dimension:
		return 1, nil
	case e.ModelVersion == legacyVersion && len(e.Vector) == Dimension+10:
		return .9, nil
	default:
		return 0, fmt.Errorf("reid: incompatible embedding version or dimensions")
	}
}

// Validate checks the part used for retrieval once on import.
func Validate(e inference.EmbeddingResult) error {
	want, err := squaredVisualNorm(e)
	if err != nil {
		return err
	}
	var sum float64
	n := min(Dimension, len(e.Vector))
	if IsCushkiVersion(e.ModelVersion) {
		n = len(e.Vector)
	}
	for _, x := range e.Vector[:n] {
		sum += float64(x) * float64(x)
	}
	if !(math.Abs(sum-want) < 1e-5) {
		return fmt.Errorf("reid: invalid visual norm")
	}
	return nil
}

// IsSilhouette identifies versions whose foreground was modified before
// encoding: real silhouette masks and the central-crop fallback. Similarity
// keeps each of these spaces separate from raw bounding-box descriptors.
func IsSilhouette(version string) bool {
	return version == Version || version == publishedHeadsVersion || version == centralVersion
}

// Similarity keeps different foreground preprocessors in separate retrieval spaces.
// The two historical bounding-box formats share the same visual dimensions.
// CUshki-версии образуют отдельное пространство: кросс megadescriptor/cushki
// и разные длины векторов отвергаются явно.
func Similarity(a, b inference.EmbeddingResult) (float64, error) {
	if a.ModelVersion != b.ModelVersion && (IsSilhouette(a.ModelVersion) || IsSilhouette(b.ModelVersion)) {
		return 0, fmt.Errorf("reid: incompatible silhouettes or bounding-box descriptors; re-encode both inputs with the same masks")
	}
	if IsCushkiVersion(a.ModelVersion) != IsCushkiVersion(b.ModelVersion) {
		return 0, fmt.Errorf("reid: incompatible embedding versions %q and %q; re-encode both inputs with the same model", a.ModelVersion, b.ModelVersion)
	}
	na, err := squaredVisualNorm(a)
	if err != nil {
		return 0, err
	}
	nb, err := squaredVisualNorm(b)
	if err != nil {
		return 0, err
	}
	n := Dimension
	if IsCushkiVersion(a.ModelVersion) {
		if len(a.Vector) != len(b.Vector) {
			return 0, fmt.Errorf("reid: incompatible vector lengths %d and %d", len(a.Vector), len(b.Vector))
		}
		n = len(a.Vector)
	}
	var dot float64
	for i := range n {
		dot += float64(a.Vector[i]) * float64(b.Vector[i])
	}
	score := dot / math.Sqrt(na*nb)
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0, fmt.Errorf("reid: invalid cosine")
	}
	return min(1, max(-1, score)), nil
}

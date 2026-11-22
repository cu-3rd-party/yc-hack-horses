package yolo

import (
	"image"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
)

type Config struct {
	ModelPath      string
	RuntimeLibPath string
	// ReIDModelDir enables visual retrieval and separate per-instance measurements.
	ReIDModelDir string
	// ReIDModelPath — путь к ONNX-модели CUshki v6 (динамический dim, вход
	// 448x448). Несколько членов ансамбля — через разделитель ";". Задан —
	// используется cushki-энкодер вместо megadescriptor из ReIDModelDir.
	ReIDModelPath string
	// ReIDModelInputSize — сторона входа cushki-модели (0 = 448).
	ReIDModelInputSize int
	IncludeMasks  bool
	ConfThreshold float64
	IouThreshold  float64
	Blobs         inference.BlobFetcher
	Delegate      TraitDelegate
	// DetectionClasses — фильтр COCO-классов детектора; пусто — набор
	// животных из zapovednik (animalClassIDs). Переопределяется для
	// строгого режима {horseClassID}.
	DetectionClasses []int
	// DualVariant — включить двухвариантное кодирование (raw + masked,
	// приём против фонового переобучения из zapovednik-public). По умолчанию
	// выключено: старое поведение (только raw) сохраняется для безопасного
	// деплоя; masked-эмбеддинг попадает в MaskedEmbedding.
	DualVariant bool
	// FusionConfig — внешние коэффициенты скоринга (см. reid.FusionConfig).
	// nil — сырой фьюжн-скор (дефолт, поведение без калибровки).
	FusionConfig *reid.FusionConfig
	// CushkiConfig — конфиг CUshki v6 (fusion_config.json: матричная
	// логистическая голова, классы, open-set порог; см. reid.CushkiConfig).
	// nil — скоринг чистым косинусом без калибровки.
	CushkiConfig *reid.CushkiConfig
}

type InstanceEmbedding struct {
	Confidence float64 // YOLO score for automatic detections.
	Box        image.Rectangle
	Embedding  inference.EmbeddingResult
	// MaskedEmbedding — второй вариант кодирования (фон залит серым по
	// маске силуэта, либо центральный кроп в fallback-версии). Заполняется
	// только при cfg.DualVariant; иначе nil (старое поведение).
	MaskedEmbedding  *inference.EmbeddingResult
	MaskPNG          []byte
	Traits           *reid.TraitEvidence
	MeasurementError string
}

// ModelVersion — версия связки модель+эвристика. Меняется при смене весов.
const ModelVersion = "yolov8n-coco-v1"

const (
	inputW       = 640 // вход тензора YOLO
	inputH       = 640
	numClasses   = 80 // COCO
	numChannels  = 84 // 4 координаты + 80 классов (YOLOv8 после NMS-free head)
	numAnchors   = 8400
	horseClassID = 17 // COCO class "horse"
	// headFractionPct — голова как верхняя доля bounding box в процентах
	// (эвристика кропа).
	headFractionPct = 30
	// headWidthPct — ширина кропа головы в процентах ширины бокса.
	headWidthPct = 60
	// cropMarginPct — запас вокруг bounding box при кропе (проценты стороны).
	cropMarginPct = 5
)

// animalClassIDs — COCO-классы животных: лошади Пржевальского детектор
// нередко путает с коровой/овцой/зеброй (коренастое тело, саврасая масть,
// полосы на ногах). Как в zapovednik-public (ml/reid/detect.py,
// COCO_ANIMAL_IDS) принимаем эти классы и трактуем их как "лошадь".
var animalClassIDs = []int{17, 18, 19, 22} // horse, sheep, cow, zebra

// Настройки по умолчанию; переопределяются Config.
const (
	DefaultConfThreshold = 0.25
	DefaultIouThreshold  = 0.45
)

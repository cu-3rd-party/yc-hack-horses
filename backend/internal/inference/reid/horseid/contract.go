// Package horseid: ReID пайплайн horseid-dinov2-arcface-v2 (YOLO11s → ViTPose+ → DINOv2-B → ArcFace-проекция).
// Контракт координатора: скелет типов и констант для параллельных модулей.
// Менять файлы этого пакета можно только в своей зоне (см. распределение в HANDOFF_GO.md).
package horseid

// Version — версия дескриптора, под ней лежат эталоны (gallery_*.f32) и векторы запроса.
const Version = "horseid-dinov2-arcface-v2"

// Параметры пайплайна из HANDOFF_GO.md / meta.json.
const (
	// Temp — температура softmax для процентов (не калибрована).
	Temp = 20.0
	// WeightHead — вес head-части при слиянии score = w*head + (1-w)*body.
	WeightHead = 0.5
	// EmbedDim — размер проекции ArcFace (head и body по 256).
	EmbedDim = 256
	// DinoDim — выход DINOv2-B (CLS после norm).
	DinoDim = 768
	// MaxSide — фото уменьшается до этого размера по длинной стороне до детекции.
	MaxSide = 1280
	// YoloConf — порог уверенности YOLO (класс 17 horse).
	YoloConf = 0.25
	// YoloImgsz — сторона входа YOLO.
	YoloImgsz = 640
	// KpScoreMin — порог скор ключевых точек ViTPose.
	KpScoreMin = 0.3
	// HeadSideScale — сторона head-кропа = scale * max(ширина, высота) bbox точек головы.
	HeadSideScale = 1.6
	// HeadSideMin — минимальная сторона head-кропа в пикселях.
	HeadSideMin = 48
	// BodyMargin — запас body-кропа вокруг бокса в пикселях (±8 px).
	BodyMargin = 8
	// AggTop2 — агрегация сходств по лошади: среднее двух лучших фото.
	AggTop2 = "top2"
	// ViewSideThresh — порог смещения носа относительно центра бёдер (доли ширины бокса).
	ViewSideThresh = 0.25
)

// KeyPoint — ключевая точка ViTPose в координатах исходного (уменьшенного) фото.
type KeyPoint struct {
	X, Y  float64
	Score float64
}

// View — ракурс лошади (только для UI, в ранжировании не используется).
type View string

const (
	ViewLeft    View = "left"
	ViewRight   View = "right"
	ViewFront   View = "front"
	ViewRear    View = "rear"
	ViewUnknown View = "unknown"
)

// Detection — результат этапа детекции/позы для одного фото.
type Detection struct {
	// BodyBox — самый большой бокс лошади (x0,y0,x1,y1) в координатах уменьшенного фото.
	BodyBox [4]float64
	// HeadBox — квадратный head-бокс или nil, если голову найти не удалось.
	HeadBox *[4]float64
	// Keypoints — 17 точек ViTPose (индексы COCO AP-10K).
	Keypoints []KeyPoint
	// View — ракурс.
	View View
	// NumHorses — сколько боксов дал YOLO.
	NumHorses int
}

// Embedding — ArcFace-проекции кропов. Head и Body — векторы по 256, L2-нормализованные.
// NaN-семантика недопустима: отсутствующая часть обозначается HasHead/HasBody=false.
type Embedding struct {
	Head    []float64
	Body    []float64
	HasHead bool
	HasBody bool
}

// Detector находит лошадь и ключевые точки (YOLO11s + ViTPose+).
type Detector interface {
	// Detect принимает исходные байты изображения (уже с EXIF-поворотом и ресайзом до MaxSide —
	// ресайз выполняет реализация, см. PreprocessImage).
	Detect(img []byte) (*Detection, error)
}

// Embedder считает ArcFace-проекции кропов (DINOv2-B + головы из export/).
type Embedder interface {
	// EmbedBody принимает body-кроп, EmbedHead — head-кроп (PNG/JPEG байты).
	EmbedHead(crop []byte) ([]float64, error)
	EmbedBody(crop []byte) ([]float64, error)
}

// Pipeline — полный путь фото до Embedding (детекция + кропы + эмбеддинги).
type Pipeline interface {
	Detector
	Embedder
}

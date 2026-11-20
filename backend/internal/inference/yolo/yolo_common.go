package yolo

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

// Настройки по умолчанию; переопределяются Config.
const (
	DefaultConfThreshold = 0.25
	DefaultIouThreshold  = 0.45
)

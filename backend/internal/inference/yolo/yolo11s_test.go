package yolo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDecodeOutputYOLO11sStyle — экспорт YOLO11s даёт тот же выход 1x84x8400,
// что и YOLOv8 (вход images, выход output0), поэтому decodeOutput работает без
// правок. Проверяем геометрию бокса класса horse и уверенность.
func TestDecodeOutputYOLO11sStyle(t *testing.T) {
	data := make([]float32, numChannels*numAnchors)
	const anchor = 42
	const cx, cy, w, h, conf = float32(300), float32(200), float32(120), float32(240), float32(0.87)
	data[anchor] = cx
	data[numAnchors+anchor] = cy
	data[2*numAnchors+anchor] = w
	data[3*numAnchors+anchor] = h
	data[(4+horseClassID)*numAnchors+anchor] = conf

	dets := decodeOutput(data, horseClassID, 0.25)
	require.Len(t, dets, 1)
	require.InDelta(t, 0.87, dets[0].Confidence, 1e-6)
	require.Equal(t, horseClassID, dets[0].ClassID)
	require.Equal(t, box{240, 80, 360, 320}, dets[0].Box)
}

// TestDecodeOutputYOLO11sLargestBox — на выходе YOLO11s может быть несколько
// лошадей; decodeOutput отдаёт все, отсортированными по уверенности, а выбор
// самого большого бокса (body) делает вызывающий код — проверяем, что боксы и
// сортировка сохраняются.
func TestDecodeOutputYOLO11sLargestBox(t *testing.T) {
	data := make([]float32, numChannels*numAnchors)
	set := func(anchor int, cx, cy, w, h, conf float32) {
		data[anchor] = cx
		data[numAnchors+anchor] = cy
		data[2*numAnchors+anchor] = w
		data[3*numAnchors+anchor] = h
		data[(4+horseClassID)*numAnchors+anchor] = conf
	}
	set(0, 300, 300, 40, 60, 0.95)   // мелкий, уверенный
	set(1, 400, 400, 200, 300, 0.60) // крупный, менее уверенный

	dets := decodeOutput(data, horseClassID, 0.25)
	require.Len(t, dets, 2)
	require.Greater(t, dets[0].Confidence, dets[1].Confidence)
	// Самый большой по площади — второй в списке (уверенность ниже).
	largest := dets[0]
	for _, d := range dets[1:] {
		if d.Box.area() > largest.Box.area() {
			largest = d
		}
	}
	require.Equal(t, box{300, 250, 500, 550}, largest.Box)
}

// TestResolveModelVersion — версия распознаётся по имени файла и метаданным,
// существующие версии не ломаются.
func TestResolveModelVersion(t *testing.T) {
	cases := []struct {
		path, desc, want string
	}{
		{"yolo11s.onnx", "", ModelVersionYOLO11s},
		{"/opt/models/YOLO11s.onnx", "", ModelVersionYOLO11s},
		{"yolo11n-pose.onnx", "", ModelVersionYOLO11s},
		{"yolo26x.onnx", "", ModelVersionYOLO26x},
		{"yolov8n.onnx", "", ModelVersion},
		{"", "", ModelVersion},
		{"model.onnx", `{"task":"detect","model":"YOLO11s"}`, ModelVersionYOLO11s},
		{"model.onnx", `{"model":"yolo26x"}`, ModelVersionYOLO26x},
		{"model.onnx", `{"task":"detect","model":"YOLOv8n"}`, ModelVersion},
		{"model.onnx", "", ModelVersion},
		{"yolo11s.onnx", "irrelevant", ModelVersionYOLO11s},
	}
	for _, c := range cases {
		require.Equal(t, c.want, resolveModelVersion(c.path, c.desc), "path=%q desc=%q", c.path, c.desc)
	}
}

// putVarint кодирует base-128 varint.
func putVarint(buf *[]byte, v uint64) {
	for v >= 0x80 {
		*buf = append(*buf, byte(v)|0x80)
		v >>= 7
	}
	*buf = append(*buf, byte(v))
}

// putLenField добавляет поле length-delimited (wire type 2).
func putLenField(buf *[]byte, field int, payload []byte) {
	putVarint(buf, uint64(field<<3|2))
	putVarint(buf, uint64(len(payload)))
	*buf = append(*buf, payload...)
}

// onnxWithDescription собирает минимальный ModelProto с metadata_props
// (поле 14) и записью description, как это делает экспорт ultralytics.
func onnxWithDescription(desc string) []byte {
	entry := []byte{}
	putLenField(&entry, 1, []byte("description"))
	putLenField(&entry, 2, []byte(desc))
	proto := []byte{}
	putVarint(&proto, uint64(1<<3|0)) // ir_version (varint), пропускается
	putVarint(&proto, 8)
	putLenField(&proto, 7, []byte("graph-placeholder")) // graph, пропускается
	putLenField(&proto, 14, entry)                      // metadata_props
	return proto
}

// TestOnnxDescriptionFromProto — разбор metadata_props["description"].
func TestOnnxDescriptionFromProto(t *testing.T) {
	desc := `{"task":"detect","model":"YOLO11s","imgsz":[640,640]}`
	require.Equal(t, desc, onnxDescriptionFromProto(onnxWithDescription(desc)))
	// Без метаданных — пусто.
	require.Empty(t, onnxDescriptionFromProto([]byte{0x08, 0x08}))
	require.Empty(t, onnxDescriptionFromProto(nil))
}

// TestModelVersionForPathReadsMetadata — версия берётся из имени файла и/или
// метаданных description реального файла.
func TestModelVersionForPathReadsMetadata(t *testing.T) {
	dir := t.TempDir()
	// Имя без версии, версия — только в метаданных.
	metaPath := filepath.Join(dir, "detector.onnx")
	require.NoError(t, os.WriteFile(metaPath, onnxWithDescription(`{"model":"YOLO11s"}`), 0o644))
	require.Equal(t, ModelVersionYOLO11s, modelVersionForPath(metaPath))

	// Имя yolo11s.onnx без читаемых метаданных.
	require.Equal(t, ModelVersionYOLO11s, modelVersionForPath(filepath.Join(dir, "yolo11s.onnx")))
	// Несуществующий файл yolov8n — значение по умолчанию.
	require.Equal(t, ModelVersion, modelVersionForPath(filepath.Join(dir, "yolov8n.onnx")))
}

package yolo

import (
	"os"
	"path/filepath"
	"strings"
)

// Версии связки модель+эвристика (дескриптор model_version фото).
//
// ModelVersion (yolov8n-coco-v1) остаётся значением по умолчанию: движок на
// старом экспорте возвращает её, поэтому существующее поведение не меняется.
// Экспорт ultralytics YOLO11s даёт тот же выход 1x84x8400 (вход images, выход
// output0), что и YOLOv8, поэтому декодер и letterbox не трогаем — меняется
// только имя версии.
const (
	ModelVersionYOLO11s = "yolo11s-coco-v1"
	ModelVersionYOLO26x = "yolo26x-coco-v1"
)

// resolveModelVersion определяет версию по имени файла модели и значению
// метаданных description (Ultralytics кладёт туда JSON-описание экспорта).
// Любой из источников, содержащий YOLO11, даёт yolo11s; YOLO26 — yolo26x.
// Прочее (в т.ч. yolov8n) — значение по умолчанию ModelVersion.
func resolveModelVersion(modelPath, description string) string {
	haystack := strings.ToLower(filepath.Base(modelPath) + " " + description)
	switch {
	case strings.Contains(haystack, "yolo11"):
		return ModelVersionYOLO11s
	case strings.Contains(haystack, "yolo26"):
		return ModelVersionYOLO26x
	default:
		return ModelVersion
	}
}

// modelVersionForPath — версия по пути к ONNX-файлу: имя файла плюс значение
// metadata_props["description"] самого файла (если он читается). Путь приходит
// снаружи (inference.model_path), правок кода для yolo11s.onnx не требуется.
func modelVersionForPath(modelPath string) string {
	return resolveModelVersion(modelPath, onnxMetaDescription(modelPath))
}

// onnxMetaDescription читает metadata_props["description"] из ONNX-файла.
// Любая ошибка чтения или отсутствие поля → пустая строка: версия определится
// по имени файла, движок не падает.
func onnxMetaDescription(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return onnxDescriptionFromProto(data)
}

// onnxDescriptionFromProto ищет в ModelProto поле 14 (metadata_props,
// repeated StringStringEntryProto) и возвращает значение записи с ключом
// "description". Разбор минимальный и защитный, без google.golang.org/protobuf:
// сканируем поля верхнего уровня, в метаданных читаем две строки (key, value).
func onnxDescriptionFromProto(data []byte) string {
	for i := 0; i < len(data); {
		tag, n := readVarint(data, i)
		if n <= 0 {
			return ""
		}
		i += n
		field, wire := int(tag>>3), tag&7
		switch wire {
		case 0: // varint
			_, n = readVarint(data, i)
			if n <= 0 {
				return ""
			}
			i += n
		case 1: // 64-битное значение
			i += 8
		case 2: // length-delimited
			ln, n := readVarint(data, i)
			if n <= 0 {
				return ""
			}
			i += n
			end := i + int(ln)
			if end > len(data) || end < i {
				return ""
			}
			if field == 14 {
				if v := descriptionFromEntry(data[i:end]); v != "" {
					return v
				}
			}
			i = end
		case 5: // 32-битное значение
			i += 4
		default:
			// Группы (wire 3/4) в ONNX не встречаются — прекращаем разбор.
			return ""
		}
	}
	return ""
}

// descriptionFromEntry читает StringStringEntryProto{key=1, value=2} и
// возвращает value, если key равен "description".
func descriptionFromEntry(entry []byte) string {
	var key, value string
	for i := 0; i < len(entry); {
		tag, n := readVarint(entry, i)
		if n <= 0 {
			break
		}
		i += n
		if tag&7 != 2 {
			return ""
		}
		ln, n := readVarint(entry, i)
		if n <= 0 {
			break
		}
		i += n
		end := i + int(ln)
		if end > len(entry) || end < i {
			break
		}
		switch tag >> 3 {
		case 1:
			key = string(entry[i:end])
		case 2:
			value = string(entry[i:end])
		}
		i = end
	}
	if strings.EqualFold(key, "description") {
		return value
	}
	return ""
}

// readVarint читает base-128 varint из data начиная со смещения off.
// Возвращает значение и число прочитанных байт (0 при ошибке/обрыве).
func readVarint(data []byte, off int) (uint64, int) {
	var v uint64
	var shift uint
	for i := off; i < len(data); i++ {
		b := data[i]
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return v, i - off + 1
		}
		shift += 7
		if shift >= 64 {
			return 0, 0
		}
	}
	return 0, 0
}

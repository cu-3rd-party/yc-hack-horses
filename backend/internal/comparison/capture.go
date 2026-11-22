package comparison

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"github.com/bep/imagemeta"
)

// CaptureTime returns the camera's local DateTimeOriginal with its subsecond
// precision, or an empty string when the capture date is unknown.
func CaptureTime(data []byte) string {
	var format imagemeta.ImageFormat
	switch http.DetectContentType(data) {
	case "image/jpeg":
		format = imagemeta.JPEG
	case "image/png":
		format = imagemeta.PNG
	case "image/webp":
		format = imagemeta.WebP
	default:
		return ""
	}
	var stamp, subsec string
	_, err := imagemeta.Decode(imagemeta.Options{
		R: bytes.NewReader(data), ImageFormat: format, Sources: imagemeta.EXIF,
		ShouldHandleTag: func(t imagemeta.TagInfo) bool {
			return strings.HasPrefix(t.Namespace, "IFD0") && (t.Tag == "DateTimeOriginal" || t.Tag == "SubSecTimeOriginal")
		},
		HandleTag: func(t imagemeta.TagInfo) error {
			text, _ := t.Value.(string)
			if t.Tag == "DateTimeOriginal" {
				stamp = text
			} else {
				subsec = text
			}
			return nil
		},
	})
	if err != nil {
		return ""
	}
	t, err := time.Parse("2006:01:02 15:04:05", stamp)
	if err != nil {
		return ""
	}
	result := t.Format("2006-01-02T15:04:05")
	if subsec != "" && strings.Trim(subsec, "0123456789") == "" {
		if digits := strings.TrimRight(subsec[:min(9, len(subsec))], "0"); digits != "" {
			result += "." + digits
		}
	}
	return result
}

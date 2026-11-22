package comparison

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"image"
	"image/jpeg"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDateTimeOriginal(t *testing.T) {
	var photo bytes.Buffer
	require.NoError(t, jpeg.Encode(&photo, image.NewGray(image.Rect(0, 0, 1, 1)), nil))
	// IFD0 points to ExifIFD at offset 26. ASCII timestamp starts at offset 44.
	directory, err := hex.DecodeString("49492a0008000000010069870400010000001a00000000000000010003900200140000002c00000000000000")
	require.NoError(t, err)
	exif := append([]byte("Exif\x00\x00"), directory...)
	exif = append(exif, []byte("2025:01:31 17:05:19\x00")...)
	marker := binary.BigEndian.AppendUint16([]byte{0xff, 0xe1}, uint16(len(exif)+2))
	data := bytes.Join([][]byte{photo.Bytes()[:2], marker, exif, photo.Bytes()[2:]}, nil)
	require.Equal(t, "2025-01-31T17:05:19", CaptureTime(data))
	require.Empty(t, CaptureTime(photo.Bytes()))
}

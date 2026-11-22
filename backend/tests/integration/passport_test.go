package integration

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"

	"github.com/stretchr/testify/require"
)

func uploadPassport(t *testing.T, horseID, filename string, content []byte, studbook string) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	if studbook != "" {
		require.NoError(t, writer.WriteField("studbook_number", studbook))
	}
	require.NoError(t, writer.Close())
	req := NewRequestWithBody(t, http.MethodPut, "/api/v1/horses/"+horseID+"/passport", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return MakeRequest(t, req, NoExpectedStatus)
}

func TestHorsePassportPDF(t *testing.T) {
	horse := DecodeJSON[domain.Horse](t, MakeRequest(t,
		NewRequestWithJSON(t, http.MethodPost, "/api/v1/horses", map[string]any{"metadata": map[string]string{"name": "Беркут"}}),
		http.StatusCreated))
	passportURL := "/api/v1/horses/" + horse.ID + "/passport"

	// до загрузки паспорта — 404
	MakeRequest(t, NewRequest(t, http.MethodGet, passportURL), http.StatusNotFound)

	// не PDF отклоняется по содержимому
	resp := uploadPassport(t, horse.ID, "passport.pdf", []byte("PK\x03\x04 docx is a zip"), "")
	require.Equal(t, http.StatusUnsupportedMediaType, resp.Code, resp.Body.String())

	// неизвестная лошадь — 404
	resp = uploadPassport(t, "01900000-0000-7000-8000-0000000000ff", "x.pdf", []byte("%PDF-1.7 x"), "")
	require.Equal(t, http.StatusNotFound, resp.Code)

	pdf := []byte("%PDF-1.7\n% passport of Berkut\n%%EOF\n")
	resp = uploadPassport(t, horse.ID, `C:\scans\7706-Беркут.pdf`, pdf, "7706")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	updated := DecodeJSON[domain.Horse](t, resp)
	require.Equal(t, "Беркут", updated.Metadata["name"], "прочие метаданные сохраняются")
	require.Equal(t, "7706", updated.Metadata[domain.MetaStudbookNumber])
	require.Equal(t, "7706-Беркут.pdf", updated.Metadata[domain.MetaPassportFilename], "путь отрезается")
	sha := updated.Metadata[domain.MetaPassportSHA256]
	require.Len(t, sha, 64)

	// GET отдаёт тот же PDF для просмотра в браузере
	got := MakeRequest(t, NewRequest(t, http.MethodGet, passportURL), http.StatusOK)
	require.Equal(t, pdf, got.Body.Bytes())
	require.Equal(t, "application/pdf", got.Header().Get("Content-Type"))
	require.Contains(t, got.Header().Get("Content-Disposition"), "inline;")
	require.Contains(t, got.Header().Get("Content-Disposition"), "filename*=UTF-8''7706-%D0%91")
	require.Equal(t, `"`+sha+`"`, got.Header().Get("ETag"))

	// условный запрос
	cond := NewRequest(t, http.MethodGet, passportURL)
	cond.Header.Set("If-None-Match", `"`+sha+`"`)
	MakeRequest(t, cond, http.StatusNotModified)

	// паспорт виден в карточке лошади
	fetched := DecodeJSON[domain.Horse](t, MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/horses/"+horse.ID), http.StatusOK))
	require.Equal(t, sha, fetched.Metadata[domain.MetaPassportSHA256])

	// замена паспорта: новый файл, номер по племкниге не затирается пустым полем
	pdf2 := []byte("%PDF-1.7\n% passport v2\n%%EOF\n")
	resp = uploadPassport(t, horse.ID, "berkut-v2.pdf", pdf2, "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	replaced := DecodeJSON[domain.Horse](t, resp)
	require.NotEqual(t, sha, replaced.Metadata[domain.MetaPassportSHA256])
	require.Equal(t, "7706", replaced.Metadata[domain.MetaStudbookNumber])
	got = MakeRequest(t, NewRequest(t, http.MethodGet, passportURL), http.StatusOK)
	require.Equal(t, pdf2, got.Body.Bytes())

	// удаление блоба через /blobs не стирает файл прикреплённого паспорта
	sha2 := replaced.Metadata[domain.MetaPassportSHA256]
	del := MakeRequest(t, NewRequest(t, http.MethodDelete, "/api/v1/blobs/"+sha2), NoExpectedStatus)
	require.Less(t, del.Code, 300, del.Body.String())
	got = MakeRequest(t, NewRequest(t, http.MethodGet, passportURL), http.StatusOK)
	require.Equal(t, pdf2, got.Body.Bytes())
}

package integration

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"

	"github.com/stretchr/testify/require"
)

// uploadTestBlob загружает файл через multipart и возвращает блоб из ответа.
func uploadTestBlob(t *testing.T, content []byte) (*httptest.ResponseRecorder, domain.Blob) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "photo.jpg")
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req := NewRequestWithBody(t, http.MethodPost, "/api/v1/blobs", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp := MakeRequest(t, req, NoExpectedStatus)
	return resp, DecodeJSON[domain.Blob](t, resp)
}

// Удаление блоба: событие blob.deleted, 404 в API, инвалидация наблюдений,
// идемпотентность и рабочий ре-аплоад.
func TestBlobDeleteEvent(t *testing.T) {
	content := []byte("photo deleted through the event journal " + time.Now().UTC().Format(time.RFC3339Nano))
	first, blob := uploadTestBlob(t, content)
	require.Equal(t, http.StatusCreated, first.Code)

	horse := DecodeJSON[domain.Horse](t, MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/horses", map[string]any{}), http.StatusCreated))
	obs := DecodeJSON[domain.Observation](t, MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/observations", map[string]any{
		"horse_id":    horse.ID,
		"location":    "50.01,30.02",
		"observer_id": "watcher-delete",
		"blob_ids":    []string{blob.SHA256},
	}), http.StatusCreated))
	require.False(t, obs.Invalidated)

	// DELETE → 204, событие записано, файл под ссылкой остаётся, блоб скрыт.
	MakeRequest(t, NewRequest(t, http.MethodDelete, "/api/v1/blobs/"+blob.SHA256+"?reason=test+cleanup"), http.StatusNoContent)

	MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/blobs/"+blob.SHA256), http.StatusNotFound)

	list := DecodeJSON[[]domain.Observation](t,
		MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/observations?horse_id="+horse.ID), http.StatusOK))
	require.Len(t, list, 1)
	require.Equal(t, obs.ID, list[0].ID)
	require.True(t, list[0].Invalidated)
	require.NotNil(t, list[0].InvalidatedAt)
	require.Equal(t, "blob deleted: "+blob.SHA256+" (test cleanup)", list[0].InvalidationReason)

	// Повторный DELETE уже удалённого — 404 (событие не дублируется).
	MakeRequest(t, NewRequest(t, http.MethodDelete, "/api/v1/blobs/"+blob.SHA256), http.StatusNotFound)

	// Некорректный sha256 — 400.
	MakeRequest(t, NewRequest(t, http.MethodDelete, "/api/v1/blobs/not-a-sha256"), http.StatusBadRequest)

	// Наблюдение, созданное после удаления, сразу помечается неактуальным.
	late := DecodeJSON[domain.Observation](t, MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/observations", map[string]any{
		"horse_id":    horse.ID,
		"observer_id": "watcher-late",
		"blob_ids":    []string{blob.SHA256},
	}), http.StatusCreated))
	require.True(t, late.Invalidated)

	// Ре-аплоад того же файла после удаления снова регистрирует блоб (201).
	second, reuploaded := uploadTestBlob(t, content)
	require.Equal(t, http.StatusCreated, second.Code)
	require.Equal(t, blob.SHA256, reuploaded.SHA256)

	resp := MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/blobs/"+blob.SHA256), http.StatusOK)
	require.True(t, bytes.Equal(content, resp.Body.Bytes()))

	// Удаление необратимо для наблюдений: ре-аплоад не сбрасывает invalidated.
	list = DecodeJSON[[]domain.Observation](t,
		MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/observations?horse_id="+horse.ID), http.StatusOK))
	require.Len(t, list, 2)
	for _, item := range list {
		require.True(t, item.Invalidated)
	}
}

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestHealthAndReady(t *testing.T) {
	resp := MakeRequest(t, NewRequest(t, http.MethodGet, "/health"), http.StatusOK)
	require.Equal(t, `{"status":"ok"}`+"\n", resp.Body.String())

	MakeRequest(t, NewRequest(t, http.MethodGet, "/readyz"), http.StatusOK)
}

func TestUnknownRouteJSON404(t *testing.T) {
	resp := MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/nope"), http.StatusNotFound)
	require.Contains(t, resp.Body.String(), "not found")
	require.Contains(t, resp.Header().Get("Content-Type"), "application/json")
}

func TestHorseLifecycle(t *testing.T) {
	// POST → 201 + Location
	resp := MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/horses",
		map[string]any{"metadata": map[string]string{"note": "первая встреча"}}), http.StatusCreated)
	horse := DecodeJSON[domain.Horse](t, resp)
	require.NotEmpty(t, horse.ID)
	require.Equal(t, "active", horse.IdentityStatus)
	require.Equal(t, "/api/v1/horses/"+horse.ID, resp.Header().Get("Location"))

	// GET by id
	horse2 := DecodeJSON[domain.Horse](t,
		MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/horses/"+horse.ID), http.StatusOK))
	require.Equal(t, horse.ID, horse2.ID)

	// PATCH → 200 + изменённые поля
	resp = MakeRequest(t, NewRequestWithJSON(t, http.MethodPatch, "/api/v1/horses/"+horse.ID,
		map[string]any{"metadata": map[string]string{"region": "Chernobyl"}}), http.StatusOK)
	patched := DecodeJSON[domain.Horse](t, resp)
	require.Equal(t, "Chernobyl", patched.Metadata["region"])
	require.Equal(t, "первая встреча", patched.Metadata["note"])

	// PATCH несуществующей → 404
	MakeRequest(t, NewRequestWithJSON(t, http.MethodPatch, "/api/v1/horses/00000000-0000-7000-8000-0000000000ff",
		map[string]any{"identity_status": "merged"}), http.StatusNotFound)

	// кривой JSON → 400
	MakeRequest(t, NewRequestWithBody(t, http.MethodPost, "/api/v1/horses", bytes.NewReader([]byte("{oops"))), http.StatusBadRequest)

	// список содержит созданную
	list := DecodeJSON[[]domain.Horse](t, MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/horses"), http.StatusOK))
	require.Len(t, list, 1)
}

func TestObservations(t *testing.T) {
	horse := DecodeJSON[domain.Horse](t, MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/horses", map[string]any{}), http.StatusCreated))

	obs := DecodeJSON[domain.Observation](t, MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/observations", map[string]any{
		"horse_id":    horse.ID,
		"location":    "50.01,30.02",
		"observer_id": "watcher-1",
		"comment":     "  На лугу.\nУ реки.  ",
		"blob_ids":    []string{},
	}), http.StatusCreated))
	require.Equal(t, horse.ID, obs.HorseID)
	require.Equal(t, "watcher-1", obs.ObserverID)
	require.Equal(t, "На лугу.\nУ реки.", obs.Comment)

	// несуществующая лошадь → 404
	MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/observations", map[string]any{
		"horse_id": "00000000-0000-7000-8000-0000000000ff", "observer_id": "w",
	}), http.StatusNotFound)

	// без observer_id → 422
	MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/observations", map[string]any{}), http.StatusUnprocessableEntity)

	// фильтр по horse_id
	list := DecodeJSON[[]domain.Observation](t,
		MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/observations?horse_id="+horse.ID), http.StatusOK))
	require.Len(t, list, 1)
	require.Equal(t, obs.ID, list[0].ID)
	require.Equal(t, obs.Comment, list[0].Comment)
}

func TestBlobsUploadDeduplication(t *testing.T) {
	upload := func(content []byte) *httptest.ResponseRecorder {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, err := writer.CreateFormFile("file", "photo.jpg")
		require.NoError(t, err)
		_, err = part.Write(content)
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		req := NewRequestWithBody(t, http.MethodPost, "/api/v1/blobs", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		return MakeRequest(t, req, NoExpectedStatus)
	}
	content := []byte("fake jpeg bytes for przewalski horse")
	first := upload(content)
	require.Equal(t, http.StatusCreated, first.Code)
	blob := DecodeJSON[domain.Blob](t, first)

	// повторная загрузка того же — 200, тот же sha256
	second := upload(content)
	require.Equal(t, http.StatusOK, second.Code)
	dup := DecodeJSON[domain.Blob](t, second)
	require.Equal(t, blob.SHA256, dup.SHA256)

	// GET по sha256 возвращает содержимое
	resp := MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/blobs/"+blob.SHA256), http.StatusOK)
	require.True(t, bytes.Equal(content, resp.Body.Bytes()))
	require.Equal(t, `"`+blob.SHA256+`"`, resp.Header().Get("ETag"))

	// неизвестный хэш → 404
	unknown := "0000000000000000000000000000000000000000000000000000000000000000"
	MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/blobs/"+unknown), http.StatusNotFound)
}

func TestNodeInfo(t *testing.T) {
	info := DecodeJSON[map[string]any](t, MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/node"), http.StatusOK))
	require.Equal(t, "01900000-0000-7000-8000-000000000001", info["node_id"])
	require.Contains(t, info, "event_count")
	require.Contains(t, info, "last_seq")
	require.Contains(t, info, "vector_clock")
}

// TestEventsWS: первый кадр — векторные часы, далее события в реальном времени.
func TestEventsWS(t *testing.T) {
	server := httptest.NewServer(testRoutes)
	defer server.Close()

	wsURL := "ws" + server.URL[len("http"):] + "/api/v1/events"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	// кадр часов
	_, clockFrame, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(clockFrame), `"type":"clock"`)

	// The subscription must already be live when the client receives the clock.
	result := MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/observations", map[string]any{
		"observer_id": "ws-tester",
	}), http.StatusCreated)
	observation := DecodeJSON[domain.Observation](t, result)
	_, eventFrame, err := conn.Read(ctx)
	require.NoError(t, err)
	var frame struct {
		Type  string       `json:"type"`
		Event domain.Event `json:"event"`
	}
	require.NoError(t, json.Unmarshal(eventFrame, &frame))
	require.Equal(t, "event", frame.Type)
	require.Equal(t, domain.EventObservationAdded, frame.Event.EventType)
	require.Equal(t, observation.ID, frame.Event.AggregateID)
}

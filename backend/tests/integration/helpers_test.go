// Интеграционные тесты: весь роутер в процессе через httptest (стиль шаблона
// backend-rest-go). Вспомогательные хелперы запросов — здесь.
package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// NoExpectedStatus отключает проверку статуса в MakeRequest.
const NoExpectedStatus = -1

// NewRequest строит запрос без тела.
func NewRequest(t testing.TB, method, urlStr string) *http.Request {
	t.Helper()
	return NewRequestWithBody(t, method, urlStr, nil)
}

// NewRequestWithJSON кодирует v как JSON-тело.
func NewRequestWithJSON(t testing.TB, method, urlStr string, v any) *http.Request {
	t.Helper()
	body, err := json.Marshal(v)
	require.NoError(t, err)
	req := NewRequestWithBody(t, method, urlStr, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// NewRequestWithBody строит запрос с произвольным телом.
func NewRequestWithBody(t testing.TB, method, urlStr string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, urlStr, body)
	require.NoError(t, err)
	req.RequestURI = urlStr
	return req
}

// MakeRequest исполняет запрос на роутере и проверяет статус.
func MakeRequest(t testing.TB, req *http.Request, expectedStatus int) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	testRoutes.ServeHTTP(recorder, req)
	if expectedStatus != NoExpectedStatus {
		require.Equal(t, expectedStatus, recorder.Code, "status mismatch for %s %s: %s",
			req.Method, req.URL.Path, recorder.Body.String())
	}
	return recorder
}

// DecodeJSON разбирает тело ответа в T.
func DecodeJSON[T any](t testing.TB, resp *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &v), "body: %s", resp.Body.String())
	return v
}

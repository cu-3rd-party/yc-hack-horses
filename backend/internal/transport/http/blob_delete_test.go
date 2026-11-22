// blob_delete_test.go: HTTP-тесты удаления блоба (blob.deleted). Часы
// подменяются через Deps.Clock, чтобы смоделировать устаревшее удаление:
// регистрация идёт по реальному времени, delete-хендлер — по подменённому.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/fsblob"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
	migrations "github.com/cu3rd/yc-hack-horses/backend/migrations"
)

// newBlobDeleteEnv поднимает роутер на sqlite с управляемыми часами.
func newBlobDeleteEnv(t *testing.T, clock func() time.Time) (http.Handler, *sqlstore.Store, *fsblob.Store) {
	t.Helper()
	store, err := sqlstore.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	require.NoError(t, migrate.ApplyFS(context.Background(), store.DB(), migrations.FS(), "sqlite"))
	blobs, err := fsblob.New(t.TempDir())
	require.NoError(t, err)
	handler := NewRouter(Deps{Store: store, Blobs: blobs, NodeID: "test-node", Clock: clock})
	return handler, store, blobs
}

// uploadBlobForDelete грузит файл через multipart и возвращает sha256.
func uploadBlobForDelete(t *testing.T, handler http.Handler, data []byte) string {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, err := w.CreateFormFile("file", "photo.jpg")
	require.NoError(t, err)
	_, err = fw.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/blobs", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var blob struct {
		SHA256 string `json:"sha256"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &blob))
	require.NotEmpty(t, blob.SHA256)
	return blob.SHA256
}

// Устаревшее удаление (часы delete-хендлера отстают от регистрации): проекция —
// no-op (guard registered_at <= deletedAt не проходит), поэтому DELETE отдаёт 404,
// а файл ЖИВОГО блоба остаётся на диске. Регресс: безусловный PruneBlobs удалял
// бы файл живого блоба.
func TestDeleteBlob_StaleEventKeepsLiveFile(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	handler, store, blobs := newBlobDeleteEnv(t, func() time.Time { return past })
	ctx := context.Background()
	sha := uploadBlobForDelete(t, handler, []byte("photo under stale clock"))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/blobs/"+sha+"?reason=stale", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())

	_, err := store.GetBlobMeta(ctx, sha)
	require.NoError(t, err, "устаревшее удаление не должно скрывать живой блоб")
	present, err := blobs.Has(ctx, sha)
	require.NoError(t, err)
	require.True(t, present, "файл живого блоба не должен удаляться при устаревшем удалении")
}

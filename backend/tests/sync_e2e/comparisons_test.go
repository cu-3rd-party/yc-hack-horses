package sync_e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
	"github.com/cu3rd/yc-hack-horses/backend/internal/config"
	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// In PostgreSQL runs only the primary node changes driver. The peer always
// uses SQLite, so replication exercises the on-wire event and blob formats.
func TestComparisonAcrossNodes(t *testing.T) {
	t.Run("SQLite", func(t *testing.T) { exerciseComparison(t, "") })
	t.Run("Postgres", func(t *testing.T) {
		dsn := os.Getenv("TEST_PG_DSN")
		if dsn == "" {
			t.Skip("set TEST_PG_DSN to test PostgreSQL")
		}
		db, err := sql.Open("pgx", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })
		schema := "review_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		_, err = db.Exec("CREATE SCHEMA " + schema)
		require.NoError(t, err)
		t.Cleanup(func() { _, err := db.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) })
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		exerciseComparison(t, u.String())
	})
}

func reviewRequest(t *testing.T, method, url string, body any, status int, out any) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(method, url, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, status, resp.StatusCode, "%s %s: %s", method, url, data)
	if out != nil {
		require.NoError(t, json.Unmarshal(data, out))
	}
	if method == "GET" || strings.HasSuffix(url, "/comparisons") {
		require.NotContains(t, string(data), `"embedding"`)
	}
}

func exerciseComparison(t *testing.T, postgresDSN string) {
	var imageBytes bytes.Buffer
	require.NoError(t, jpeg.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 100, 50)), nil))
	var scenario atomic.Value
	scenario.Store("pair")
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Method != "POST" || !bytes.Equal(body, imageBytes.Bytes()) {
			http.Error(w, "expected original photo", 400)
			return
		}
		det := func(dx int, v []float64) map[string]any {
			return map[string]any{
				"detection_confidence": 0.97, "embedding": v, "horse_polygon": [][]int{{dx, 5}, {dx + 15, 2}, {dx + 30, 20}, {dx + 20, 40}, {dx, 30}},
				"traits": []any{map[string]any{"key": "muzzle", "value": "light", "polygon": [][]int{{dx, 5}, {dx + 3, 4}, {dx + 2, 7}}}, map[string]any{"key": "mane", "value": "upright", "polygon": nil}},
			}
		}
		dets := []map[string]any{det(5, []float64{1, 0}), det(55, []float64{0, 1})}
		switch scenario.Load() {
		case "query":
			dets[0]["embedding"] = []float64{4, 3}
		case "none":
			dets = dets[:0]
		case "bbox":
			delete(dets[0], "horse_polygon")
		case "invalid":
			dets[1]["horse_polygon"] = [][]int{{55, 5}, {70, 9}, {101, 40}}
		}
		json.NewEncoder(w).Encode(map[string]any{"model_version": "test-v1", "width": 100, "height": 50, "detections": dets})
	}))
	t.Cleanup(model.Close)
	ca := newTestCA(t)
	idA, idB := uuid.NewString(), uuid.NewString()
	grpcB := freePort(t)
	enableModel := func(c *config.Config) { c.Inference.ComparisonURL = model.URL }
	a := startNode(t, idA, t.TempDir(), ca, []config.PeerConfig{{Addr: grpcB, NodeID: idB, Host: "node-" + short(idB)}}, "", enableModel, func(c *config.Config) {
		if postgresDSN != "" {
			c.Storage.Driver = "postgres"
			c.Storage.DSN = postgresDSN
		}
	})
	b := startNode(t, idB, t.TempDir(), ca, []config.PeerConfig{{Addr: a.grpcAddr, NodeID: idA, Host: "node-" + short(idA)}}, grpcB, enableModel)
	upload := func(data []byte) domain.Blob {
		t.Helper()
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		file, err := mw.CreateFormFile("file", "photo.jpg")
		require.NoError(t, err)
		_, err = file.Write(data)
		require.NoError(t, err)
		require.NoError(t, mw.Close())
		res, err := http.Post(a.httpBase+"/api/v1/blobs", mw.FormDataContentType(), &body)
		require.NoError(t, err)
		return decodeBlob(t, res)
	}
	blob := upload(imageBytes.Bytes())
	compare := func(n *e2eNode, k, status int) comparison.Result {
		t.Helper()
		var v comparison.Result
		reviewRequest(t, "POST", n.httpBase+"/api/v1/comparisons", map[string]any{"blob_sha256": blob.SHA256, "top_k": k}, status, &v)
		return v
	}
	compare(a, 0, 422)
	scenario.Store("bbox")
	compare(a, 2, 503)
	scenario.Store("invalid")
	compare(a, 2, 502)
	scenario.Store("none")
	require.Equal(t, []comparison.Detection{}, compare(a, 2, 201).Detections)
	scenario.Store("pair")
	seed := compare(a, 2, 201)
	require.Len(t, seed.Detections, 2)
	require.NotEqual(t, seed.Detections[0].ID, seed.Detections[1].ID)
	require.Equal(t, []comparison.Candidate{}, seed.Detections[0].Candidates)
	require.Equal(t, comparison.Point{.55, .1}, seed.Detections[1].Annotation.HorsePolygon[0])
	require.Len(t, seed.Detections[1].Annotation.HorsePolygon, 5)
	require.Equal(t, comparison.Point{.58, .08}, seed.Detections[1].Annotation.Traits[0].Polygon[1])
	require.Nil(t, seed.Detections[0].Annotation.Traits[1].Polygon)
	decisionURL := func(v comparison.Result, i int) string {
		return a.httpBase + "/api/v1/comparisons/" + v.ID + "/detections/" + v.Detections[i].ID + "/decision"
	}
	horses := []string{}
	for i, name := range []string{"Сарана", "Буран"} {
		var saved comparison.Saved
		reviewRequest(t, "PUT", decisionURL(seed, i), comparison.Decision{Action: "create_horse", HorseName: name, Name: "Фото " + name}, 200, &saved)
		horses = append(horses, saved.HorseID)
	}
	scenario.Store("query")
	query := compare(a, 2, 201)
	c := query.Detections[0].Candidates
	require.Len(t, c, 2)
	require.Equal(t, horses[0], c[0].HorseID)
	require.Equal(t, "Сарана", c[0].Name)
	require.InDelta(t, .8, c[0].Similarity, 1e-12)
	require.Equal(t, seed.Detections[0].ID, c[0].BestPhotoID)
	require.InDelta(t, .6, c[1].Similarity, 1e-12)
	require.Equal(t, horses[1], query.Detections[1].Candidates[0].HorseID)
	require.Len(t, compare(a, 1, 201).Detections[0].Candidates, 1)
	decision := comparison.Decision{Action: "match", HorseID: horses[0], Name: "Повторное наблюдение"}
	bad := decision
	bad.HorseID = uuid.NewString()
	reviewRequest(t, "PUT", decisionURL(query, 0), bad, 422, nil)
	// Concurrent retries must create exactly one observation and one event.
	results := make(chan *http.Response, 2)
	errs := make(chan error, 2)
	raw, err := json.Marshal(decision)
	require.NoError(t, err)
	for range 2 {
		go func() {
			req, err := http.NewRequest("PUT", decisionURL(query, 0), bytes.NewReader(raw))
			if err != nil {
				errs <- err
				return
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errs <- err
				return
			}
			results <- resp
		}()
	}
	var saved comparison.Saved
	for i := range 2 {
		select {
		case err := <-errs:
			t.Fatal(err)
		case resp := <-results:
			other := decode[comparison.Saved](t, resp)
			if i == 0 {
				saved = other
			} else {
				require.Equal(t, saved, other)
			}
		}
	}
	require.Equal(t, []string{query.Detections[1].ID}, saved.Remaining)
	bad = decision
	bad.Name = "Другая запись"
	reviewRequest(t, "PUT", decisionURL(query, 0), bad, 409, nil)
	a.cancel()
	<-a.runErr
	a.start(t)
	cancelURL := a.httpBase + "/api/v1/comparisons/" + query.ID + "/cancel"
	var cancelled, retry comparison.Cancelled
	reviewRequest(t, "POST", cancelURL, nil, 200, &cancelled)
	reviewRequest(t, "POST", cancelURL, nil, 200, &retry)
	require.Equal(t, cancelled, retry)
	require.Equal(t, []string{saved.ObservationID}, cancelled.Saved)
	require.Equal(t, []string{query.Detections[1].ID}, cancelled.Skipped)
	reviewRequest(t, "PUT", decisionURL(query, 1), decision, 409, nil)
	var again comparison.Saved
	reviewRequest(t, "PUT", decisionURL(query, 0), decision, 200, &again)
	require.Equal(t, saved.ObservationID, again.ObservationID)
	require.Empty(t, again.Remaining)
	history := func(n *e2eNode) []comparison.History {
		var records []comparison.History
		reviewRequest(t, "GET", n.httpBase+"/api/v1/observations?include=photos,annotations&horse_id="+horses[0], nil, 200, &records)
		return records
	}
	local := history(a)
	require.Len(t, local, 2)
	for _, o := range local {
		require.Len(t, o.Photos, 1)
		p := o.Photos[0]
		require.Equal(t, blob.SHA256, p.SHA)
		require.Equal(t, &query.Detections[0].Annotation, p.Annotation)
	}
	// Manual observations can still point to legacy blob UUIDs. A second
	// original must replicate with a distinct ID and no invented geometry.
	var second bytes.Buffer
	require.NoError(t, jpeg.Encode(&second, image.NewRGBA(image.Rect(0, 0, 50, 50)), nil))
	legacy := upload(second.Bytes())
	var blobs []domain.Blob
	reviewRequest(t, "GET", a.httpBase+"/api/v1/blobs", nil, 200, &blobs)
	for _, b := range blobs {
		if b.SHA256 == legacy.SHA256 {
			legacy.ID = b.ID
		}
	}
	require.NotEmpty(t, legacy.ID)
	reviewRequest(t, "POST", a.httpBase+"/api/v1/observations", map[string]any{
		"horse_id": horses[0], "observer_id": "field", "blob_ids": []string{legacy.ID},
	}, 201, nil)
	local = history(a)
	require.Len(t, local, 3)
	manual := local[2].Photos
	require.Len(t, manual, 1)
	require.Equal(t, legacy.SHA256, manual[0].SHA)
	require.Nil(t, manual[0].Annotation)
	require.NotEmpty(t, manual[0].ID)
	a.syncOnce(t)
	b.syncOnce(t)
	a.syncOnce(t)
	require.Equal(t, local, history(b))
	replica := compare(b, 2, 201)
	require.Equal(t, horses[0], replica.Detections[0].Candidates[0].HorseID)
	require.Equal(t, query.Detections[0].ID, replica.Detections[0].Candidates[0].BestPhotoID)
	require.InDelta(t, 1., replica.Detections[0].Candidates[0].Similarity, 1e-12)
}

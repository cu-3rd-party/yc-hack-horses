package enroll

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// enrollServer поднимает httptest-сервер, ведущий себя как POST /api/v1/enroll
// central с сервисом, подписывающим тестовым CA.
func enrollServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	caFile, keyFile := writeTestCA(t, dir)
	svc, err := NewService(ServerConfig{Token: token, CAFile: caFile, CAKeyFile: keyFile, DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/enroll", func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"message":"bad json"}`, http.StatusBadRequest)
			return
		}
		nodePEM, caPEM, expires, err := svc.Enroll(req.NodeID, req.CSR, req.Token)
		if err != nil {
			status := http.StatusInternalServerError
			if err == ErrBadToken {
				status = http.StatusForbidden
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"` + err.Error() + `"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{
			NodeCert:  string(nodePEM),
			CACert:    string(caPEM),
			NodeID:    req.NodeID,
			ExpiresAt: expires.Format(time.RFC3339),
		})
	})
	return httptest.NewServer(mux)
}

func TestEnrollEndToEnd(t *testing.T) {
	srv := enrollServer(t, "secret-token")
	defer srv.Close()
	dataDir := t.TempDir()
	cfg := ClientConfig{CentralURL: srv.URL, DataDir: dataDir, HTTPClient: srv.Client()}
	now := time.Now()

	// Первый вызов: enrollment.
	st, err := Enroll(context.Background(), cfg, "secret-token", now)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if !st.Enrolled || st.NeedsReenroll || st.NodeID == "" {
		t.Fatalf("статус после enrollment: %+v", st)
	}
	// Файлы на месте: cert/key/ca, ключ 0600.
	for _, f := range []string{"cert.pem", "key.pem", "ca.pem", "node.json"} {
		if _, err := os.Stat(filepath.Join(dataDir, f)); err != nil {
			t.Fatalf("нет %s: %v", f, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(dataDir, "key.pem")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("права на ключ: %v %v", fi, err)
	}
	// Повторный вызов без пере-enroll: сеть не нужна (сервер закрыт) —
	// ErrAlreadyValid, статус прежний.
	srv.Close()
	st2, err := Enroll(context.Background(), cfg, "secret-token", now)
	if err != ErrAlreadyValid {
		t.Fatalf("ожидается ErrAlreadyValid, получено %v", err)
	}
	if st2.NodeID != st.NodeID {
		t.Fatalf("node_id изменился: %s != %s", st2.NodeID, st.NodeID)
	}
}

func TestEnrollBadToken(t *testing.T) {
	srv := enrollServer(t, "secret-token")
	defer srv.Close()
	cfg := ClientConfig{CentralURL: srv.URL, DataDir: t.TempDir(), HTTPClient: srv.Client()}
	st, err := Enroll(context.Background(), cfg, "wrong-token", time.Now())
	if err == nil {
		t.Fatal("enroll с неверным токеном прошёл")
	}
	if !st.NeedsReenroll {
		t.Fatalf("после отказа enrollment не выполнен: %+v", st)
	}
	// Файлы серта не записаны.
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "cert.pem")); !os.IsNotExist(err) {
		t.Fatal("серт записан при отказе")
	}
}

func TestEnrollEmptyToken(t *testing.T) {
	cfg := ClientConfig{CentralURL: "http://127.0.0.1:1", DataDir: t.TempDir()}
	if _, err := Enroll(context.Background(), cfg, "", time.Now()); err != ErrBadToken {
		t.Fatalf("ожидается ErrBadToken, получено %v", err)
	}
}

func TestLoadOrCreateNodeIDPersistent(t *testing.T) {
	dir := t.TempDir()
	id1, err := LoadOrCreateNodeID(dir)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := LoadOrCreateNodeID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if id1 == "" || id1 != id2 {
		t.Fatalf("node_id не стабилен: %s != %s", id1, id2)
	}
}

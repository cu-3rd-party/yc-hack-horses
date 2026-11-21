package modelassets

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSeparateDownloadsVerifyCancelAndRetry(t *testing.T) {
	contents := []byte("test model")
	var attempts atomic.Int32
	serving := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch attempts.Add(1) {
		case 1:
			w.Write([]byte("wrong export"))
		case 2:
			w.Write([]byte("partial"))
			w.(http.Flusher).Flush()
			close(serving)
			<-r.Context().Done()
		default:
			w.Write(contents)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	var installed atomic.Int32
	m := New(filepath.Join(dir, "yolo.onnx"), dir, server.URL, func() { installed.Add(1) })
	t.Cleanup(m.Close)
	m.client = server.Client()
	wait := func(state string) {
		t.Helper()
		require.Eventually(t, func() bool { return m.Status()[0].State == state }, 3*time.Second, time.Millisecond)
	}
	wait("missing")
	m.mu.Lock()
	m.files[0].sha = fmt.Sprintf("%x", sha256.Sum256(contents))
	m.mu.Unlock()
	require.NoError(t, m.Start("yolo", ""))
	wait("error")
	require.NoFileExists(t, filepath.Join(dir, "yolo.onnx"))
	require.Zero(t, installed.Load())
	require.NoError(t, m.Start("yolo", ""))
	select {
	case <-serving:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	require.NoError(t, m.Start("yolo", ""))
	m.Cancel("yolo")
	wait("missing")
	require.EqualValues(t, 2, attempts.Load())
	require.NoError(t, m.Start("yolo", ""))
	wait("ready")
	actual, err := os.ReadFile(filepath.Join(dir, "yolo.onnx"))
	require.NoError(t, err)
	require.Equal(t, contents, actual)
	require.EqualValues(t, 1, installed.Load())
	require.Equal(t, "missing", m.Status()[1].State)
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.NoError(t, m.Start("yolo", ""))
	require.EqualValues(t, 3, attempts.Load())
	_, err = dialPublic(context.Background(), "tcp", "127.0.0.1:8080")
	require.Error(t, err)
}

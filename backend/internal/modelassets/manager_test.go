package modelassets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
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

func TestDescriptorInstallsFromButtonAndRecognizesExistingExport(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "yolo.onnx"), dir, "", nil)
	t.Cleanup(m.Close)
	row := m.find("megadescriptor")
	require.Eventually(t, func() bool { return m.Status()[1].State == "missing" }, time.Second, time.Millisecond)
	require.Equal(t, "https://huggingface.co/BVRA/MegaDescriptor-L-384/resolve/33b3c6f4ee0c386a4126cc3dcd23843920613fa1/pytorch_model.bin", row.source)
	require.Equal(t, checkpointSHA, row.sha)
	require.True(t, m.Status()[1].HasSource)
	require.EqualValues(t, 1935177938, m.Status()[1].Size)
	require.NoFileExists(t, row.path)

	// Serve a tiny file through the same click-without-a-URL code path.
	data := []byte("checkpoint fixture")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	defer server.Close()
	row.source = server.URL
	row.sha = fmt.Sprintf("%x", sha256.Sum256(data))
	m.client = server.Client()
	require.NoError(t, m.Start("megadescriptor", ""))
	require.Eventually(t, func() bool { return m.Status()[1].State == "ready" }, time.Second, time.Millisecond)
	b, err := os.ReadFile(filepath.Join(dir, DescriptorCheckpoint))
	require.NoError(t, err)
	require.Equal(t, data, b)

	// A mirror configured for the older ONNX format must also see this installation.
	mirror := New(filepath.Join(dir, "yolo.onnx"), dir, server.URL, nil)
	t.Cleanup(mirror.Close)
	require.Eventually(t, func() bool { return mirror.Status()[1].State != "checking" }, time.Second, time.Millisecond)
	file := mirror.find("megadescriptor")
	require.Equal(t, server.URL+"/"+DescriptorONNX, file.source)
	require.Equal(t, checkpointSHA, file.alternative.sha)
	file.alternative.sha = row.sha
	mirror.check(file)
	require.Equal(t, "ready", mirror.Status()[1].State)

	// A pre-existing standalone ONNX export also works with the default source.
	require.NoError(t, os.Rename(row.path, row.alternative.path))
	row.alternative.sha = row.sha
	m.check(row)
	require.Equal(t, "ready", m.Status()[1].State)
}

func TestPartHeadsDownloadFromKaggleAndSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "part-heads.f32")
	open := func() *Manager {
		m := New(filepath.Join(dir, "yolo.onnx"), dir, "", nil)
		t.Cleanup(m.Close)
		require.Eventually(t, func() bool { return m.Status()[3].State != "checking" }, time.Second, time.Millisecond)
		return m
	}

	m := open()
	row := m.Status()[3]
	require.True(t, row.HasSource)
	require.Equal(t, "missing", row.State)
	require.NoFileExists(t, path)
	fixture, err := os.ReadFile("part-heads.f32")
	require.NoError(t, err)
	const publicURL = "https://www.kaggle.com/api/v1/datasets/download/sorrowdev/loshid-horse-part-heads/part-heads.f32?datasetVersionNumber=1"
	require.Equal(t, publicURL, m.find("part-heads").source)
	gotRequest := make(chan string, 1)
	m.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotRequest <- r.Method + " " + r.URL.String()
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(fixture)), ContentLength: int64(len(fixture))}, nil
	})}
	require.NoError(t, m.Start("part-heads", ""))
	require.Eventually(t, func() bool { return m.Status()[3].State == "ready" }, time.Second, time.Millisecond)
	require.Len(t, gotRequest, 1)
	require.Equal(t, "GET "+publicURL, <-gotRequest)
	payload, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Len(t, payload, 4*(384+3)*4)
	require.Equal(t, HeadsSHA, fmt.Sprintf("%x", sha256.Sum256(payload)))
	require.Equal(t, "ready", open().Status()[3].State)
	require.True(t, MatchesSHA(HeadsSHA, legacyHeadsSHA))
	require.False(t, MatchesSHA(DinoSHA, HeadsSHA))
	payload[0] ^= 1
	require.NoError(t, os.WriteFile(path, payload, 0600))
	require.Equal(t, "error", open().Status()[3].State)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

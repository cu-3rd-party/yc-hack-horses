// Package modelassets installs the pinned inference artifacts for a local node.
package modelassets

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DetectorSHA   = "b2bc52f40e8e1c532427d5bde3575a5d5b571b739fab2c6df443733ed1589cbd"
	DescriptorSHA = "de9d5ed23423c7f6a09e0cc033c9dc645a8b672264ad22d8d776fba470eba50f"
	DinoSHA       = "83141175ec78b4ff9a2bb58a4c7c264ba0054d1c2e122e5a8114b79a8d4179ea"
	HeadsSHA      = "a1f535819545891bd7ee01391575a9c02cdcc599da8dbf19425a980957254408"
)

type Status struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	State     string `json:"state"`
	Size      int64  `json:"size,omitempty"`
	HasSource bool   `json:"has_source"`
	Received  int64  `json:"received"`
	Total     int64  `json:"total"`
	Message   string `json:"message,omitempty"`
}
type artifact struct {
	Status
	path, sha, source string
	received          atomic.Int64
	cancel            context.CancelFunc
}
type Manager struct {
	mu        sync.Mutex
	files     []*artifact
	ctx       context.Context
	stop      context.CancelFunc
	jobs      sync.WaitGroup
	installed func()
	client    *http.Client
}

func New(detector, dir, baseURL string, installed func()) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{ctx: ctx, stop: cancel, installed: installed, client: &http.Client{Timeout: 2 * time.Hour,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || len(via) > 8 {
				return fmt.Errorf("invalid redirect")
			}
			return nil
		}}}
	for _, spec := range []struct {
		id, name, sha, origin string
		size                  int64
	}{
		{"yolo", "yolov8n.onnx", DetectorSHA, "https://github.com/ultralytics/assets/releases/download/v8.4.0/yolov8n.onnx", 12851049},
		{"megadescriptor", "megadescriptor-l-384.onnx", DescriptorSHA, "", 0},
		{"dinov2", "dinov2-small.onnx", DinoSHA, "https://huggingface.co/Xenova/dinov2-small/resolve/c2bb04a51fab207c420665f1946016107bffc701/onnx/model.onnx", 88459888},
		{"part-heads", "part-heads.f32", HeadsSHA, "", 6192},
	} {
		path := filepath.Join(dir, spec.name)
		if spec.id == "yolo" {
			path = detector
		}
		source := spec.origin
		if baseURL != "" {
			source = strings.TrimRight(baseURL, "/") + "/" + spec.name
		}
		a := &artifact{Status: Status{ID: spec.id, Filename: spec.name, State: "checking", HasSource: source != "", Size: spec.size}, path: path, sha: spec.sha, source: source}
		m.files = append(m.files, a)
	}
	m.jobs.Add(1)
	go func() {
		defer m.jobs.Done()
		for _, a := range m.files {
			m.check(a)
		}
	}()
	return m
}
func (m *Manager) check(a *artifact) {
	f, err := os.Open(a.path)
	state, message := "missing", ""
	var n int64
	if err == nil {
		h := sha256.New()
		n, err = io.Copy(h, f)
		f.Close()
		if err == nil && fmt.Sprintf("%x", h.Sum(nil)) == a.sha {
			state = "ready"
		} else {
			state = "error"
			message = "Контрольная сумма файла не совпала."
		}
	} else if !os.IsNotExist(err) {
		state = "error"
		message = "Не удалось прочитать файл модели."
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a.State, a.Message = state, message
	if state == "ready" {
		a.Total = n
		a.received.Store(n)
	}
}
func (m *Manager) Close() { m.mu.Lock(); m.stop(); m.mu.Unlock(); m.jobs.Wait() }
func (m *Manager) Status() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.files))
	for _, a := range m.files {
		s := a.Status
		s.Received = a.received.Load()
		out = append(out, s)
	}
	return out
}
func (m *Manager) find(id string) *artifact {
	for _, a := range m.files {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (m *Manager) Start(id, link string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.find(id)
	if a == nil {
		return fmt.Errorf("Неизвестная модель.")
	}
	if m.ctx.Err() != nil {
		return m.ctx.Err()
	}
	if a.State == "downloading" || a.State == "ready" || a.State == "checking" {
		return nil
	}
	customSource := link != ""
	if link == "" {
		link = a.source
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("Нужна ссылка HTTPS на файл модели.")
	}
	ctx, cancel := context.WithCancel(m.ctx)
	a.cancel = cancel
	a.State = "downloading"
	a.Message = ""
	a.Total = 0
	a.received.Store(0)
	m.jobs.Add(1)
	go func() {
		defer m.jobs.Done()
		defer cancel()
		err := m.download(ctx, a, link, customSource)
		if err == nil && m.installed != nil {
			m.installed()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		a.cancel = nil
		switch {
		case err == nil:
			a.State = "ready"
			a.Total = a.received.Load()
		case ctx.Err() != nil:
			a.State = "missing"
			a.Total = 0
			a.received.Store(0)
		default:
			a.State = "error"
			a.Message = err.Error()
		}
	}()
	return nil
}
func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a := m.find(id); a != nil && a.cancel != nil {
		a.cancel()
	}
}

type byteCounter struct{ value *atomic.Int64 }

func (c byteCounter) Write(p []byte) (int, error) { c.value.Add(int64(len(p))); return len(p), nil }
func (m *Manager) download(ctx context.Context, a *artifact, link string, customSource bool) error {
	req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
	if err != nil {
		return fmt.Errorf("Некорректный адрес файла.")
	}
	client := m.client
	if customSource {
		// An operator-configured mirror may live on the LAN. A URL typed into
		// the renderer may reach public HTTPS hosts only, including redirects.
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = dialPublic
		defer transport.CloseIdleConnections()
		client = &http.Client{Transport: transport, Timeout: m.client.Timeout, CheckRedirect: m.client.CheckRedirect}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Не удалось подключиться к источнику.")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Источник вернул HTTP %d.", resp.StatusCode)
	}
	const limit = int64(4 << 30)
	if resp.ContentLength > limit {
		return fmt.Errorf("Файл превышает 4 ГБ.")
	}
	m.mu.Lock()
	a.Total = max(resp.ContentLength, 0)
	m.mu.Unlock()
	if err = os.MkdirAll(filepath.Dir(a.path), 0700); err != nil {
		return fmt.Errorf("Не удалось создать каталог моделей.")
	}
	f, err := os.CreateTemp(filepath.Dir(a.path), ".download-*")
	if err != nil {
		return fmt.Errorf("Не удалось создать файл модели.")
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h, byteCounter{&a.received}), io.LimitReader(resp.Body, limit+1))
	closeErr := f.Close()
	if err != nil || closeErr != nil || (resp.ContentLength >= 0 && n != resp.ContentLength) {
		return fmt.Errorf("Загрузка прервалась. Повторите попытку.")
	}
	if n > limit || fmt.Sprintf("%x", h.Sum(nil)) != a.sha {
		return fmt.Errorf("Контрольная сумма не совпала. Проверьте версию файла.")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), a.path); err != nil {
		return fmt.Errorf("Не удалось сохранить модель.")
	}
	return nil
}

func dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 30 * time.Second}
	for _, entry := range addrs {
		ip := entry.IP
		if !ip.IsGlobalUnicast() || ip.IsPrivate() {
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		err = dialErr
	}
	if err == nil {
		err = fmt.Errorf("no public address")
	}
	return nil, err
}

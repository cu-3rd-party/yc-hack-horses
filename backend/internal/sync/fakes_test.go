package sync_test

import (
	"context"
	"fmt"
	"sync"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
)

// fakeEventStore — in-memory реализация контракта EventStore для тестов.
// Append идемпотентен по (node_id, sequence) — как ожидается от SQL-реализаций.
type fakeEventStore struct {
	mu     sync.Mutex
	events map[string]domain.Event // key: node_id:sequence
	order  []string
}

func newFakeEventStore() *fakeEventStore {
	return &fakeEventStore{events: map[string]domain.Event{}}
}

func key(e domain.Event) string { return fmt.Sprintf("%s:%d", e.NodeID, e.Sequence) }

func (f *fakeEventStore) Append(_ context.Context, events []domain.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range events {
		k := key(e)
		if _, ok := f.events[k]; ok {
			continue // дедупликация
		}
		f.events[k] = e
		f.order = append(f.order, k)
	}
	return nil
}

func (f *fakeEventStore) GetEvents(_ context.Context, cursor storage.Cursor, limit int) ([]domain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Event
	for _, k := range f.order {
		e := f.events[k]
		if cursor.NodeID != "" && e.NodeID != cursor.NodeID {
			continue
		}
		if e.Sequence <= cursor.Sequence {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeEventStore) GetVectorClock(_ context.Context) (storage.VectorClock, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	clock := storage.VectorClock{}
	for _, k := range f.order {
		e := f.events[k]
		clock[e.NodeID] = max(clock[e.NodeID], e.Sequence)
	}
	return clock, nil
}

func (f *fakeEventStore) GetMissingEvents(_ context.Context, remote storage.VectorClock) ([]domain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Event
	for _, k := range f.order {
		e := f.events[k]
		if e.Sequence > remote[e.NodeID] {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeEventStore) LastSequence(_ context.Context, nodeID string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var last uint64
	for _, k := range f.order {
		e := f.events[k]
		if e.NodeID == nodeID {
			last = max(last, e.Sequence)
		}
	}
	return last, nil
}

func (f *fakeEventStore) count(nodeID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, k := range f.order {
		if f.events[k].NodeID == nodeID {
			n++
		}
	}
	return n
}

// fakeBlobStore — in-memory BlobStore с дедупликацией по хэшу.
type fakeBlobStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
	puts  map[string]int
}

func newFakeBlobStore() *fakeBlobStore {
	return &fakeBlobStore{blobs: map[string][]byte{}, puts: map[string]int{}}
}

func (f *fakeBlobStore) Put(_ context.Context, sha256hex, _ string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts[sha256hex]++
	if _, ok := f.blobs[sha256hex]; ok {
		return nil // дедупликация
	}
	cp := append([]byte(nil), data...)
	f.blobs[sha256hex] = cp
	return nil
}

func (f *fakeBlobStore) Get(_ context.Context, sha256hex string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.blobs[sha256hex]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return append([]byte(nil), data...), nil
}

func (f *fakeBlobStore) Has(_ context.Context, sha256hex string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.blobs[sha256hex]
	return ok, nil
}

func (f *fakeBlobStore) Delete(_ context.Context, sha256hex string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.blobs, sha256hex)
	return nil
}

func (f *fakeBlobStore) ListMissing(_ context.Context, hashes []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, h := range hashes {
		if _, ok := f.blobs[h]; !ok {
			out = append(out, h)
		}
	}
	return out, nil
}

func (f *fakeBlobStore) Size(_ context.Context, sha256hex string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.blobs[sha256hex]
	if !ok {
		return 0, storage.ErrNotFound
	}
	return int64(len(data)), nil
}

func (f *fakeBlobStore) putCount(sha256hex string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.puts[sha256hex]
}

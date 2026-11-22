package sync_test

import (
	"context"
	"errors"
	"testing"

	syncengine "github.com/cu3rd/yc-hack-horses/backend/internal/sync"
)

// fakeNeededSource — источник потребностей в памяти; дубли и пустые строки
// специально отдаются как есть: их разбирает сам хелпер.
type fakeNeededSource struct {
	hashes []string
	err    error
}

func (f fakeNeededSource) ListActiveReferencedBlobHashes(context.Context) ([]string, error) {
	return f.hashes, f.err
}

// TestReferencedBlobHashes — потребности blob-синка: уникальные непустые хэши в
// стабильном порядке.
func TestReferencedBlobHashes(t *testing.T) {
	got, err := syncengine.ReferencedBlobHashes(context.Background(), fakeNeededSource{
		hashes: []string{"bb", "", "aa", "bb", "cc"},
	})
	if err != nil {
		t.Fatalf("referenced blob hashes: %v", err)
	}
	want := []string{"aa", "bb", "cc"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestReferencedBlobHashesError — ошибку источника не глотаем: без списка
// потребностей движок не должен молча считать, что качать нечего.
func TestReferencedBlobHashesError(t *testing.T) {
	wantErr := errors.New("store down")
	if _, err := syncengine.ReferencedBlobHashes(context.Background(), fakeNeededSource{err: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}

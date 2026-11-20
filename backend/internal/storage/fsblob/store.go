// Package fsblob: content-addressed файловое хранилище блобов.
// Пути blobs/<ab>/<остальные 62 символа hex>; дедупликация по sha256.
package fsblob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
)

// Store — реализация storage.BlobStore на локальной ФС.
type Store struct {
	root string // каталог blobs/
}

// New создаёт хранилище в dir (обычно <data_dir>/blobs).
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("fsblob: mkdir %s: %w", dir, err)
	}
	return &Store{root: dir}, nil
}

// validate требует корректный sha256 hex (64 символа).
func validate(sha256hex string) error {
	if len(sha256hex) != sha256.Size*2 {
		return fmt.Errorf("fsblob: %q: ожидался 64-символьный sha256 hex", sha256hex)
	}
	if _, err := hex.DecodeString(sha256hex); err != nil {
		return fmt.Errorf("fsblob: %q: не hex: %w", sha256hex, err)
	}
	return nil
}

// path строит двухуровневый путь: blobs/ab/cdef... Нормализуем регистр, чтобы
// данные, пришедшие с разным регистром хэша, попали в один файл.
func (s *Store) path(sha256hex string) (string, error) {
	sha256hex = strings.ToLower(sha256hex)
	if err := validate(sha256hex); err != nil {
		return "", err
	}
	return filepath.Join(s.root, sha256hex[:2], sha256hex), nil
}

// Put записывает содержимое атомарно (tmp + rename). Повторный Put того же хэша — no-op,
// при несовпадении фактического хэша данных возвращает ошибку.
func (s *Store) Put(_ context.Context, sha256hex, _ string, r []byte) error {
	p, err := s.path(sha256hex)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(r)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), sha256hex) {
		return fmt.Errorf("fsblob: put %s: хэш данных не совпадает (фактический %s)", sha256hex, hex.EncodeToString(sum[:]))
	}
	if _, err := os.Stat(p); err == nil {
		return nil // дедупликация: блоб уже есть
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("fsblob: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return fmt.Errorf("fsblob: tmpfile: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // не мешает после успешного rename
	if _, err := tmp.Write(r); err != nil {
		tmp.Close()
		return fmt.Errorf("fsblob: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fsblob: close: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("fsblob: chmod: %w", err)
	}
	if err := os.Rename(tmpName, p); err != nil {
		return fmt.Errorf("fsblob: rename: %w", err)
	}
	return nil
}

// Get возвращает содержимое или storage.ErrNotFound.
func (s *Store) Get(_ context.Context, sha256hex string) ([]byte, error) {
	p, err := s.path(sha256hex)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("fsblob: get %s: %w", sha256hex, storage.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("fsblob: get %s: %w", sha256hex, err)
	}
	return data, nil
}

// Has проверяет наличие блоба.
func (s *Store) Has(_ context.Context, sha256hex string) (bool, error) {
	p, err := s.path(sha256hex)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// Delete удаляет блоб; отсутствие — storage.ErrNotFound.
func (s *Store) Delete(_ context.Context, sha256hex string) error {
	p, err := s.path(sha256hex)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("fsblob: delete %s: %w", sha256hex, storage.ErrNotFound)
		}
		return fmt.Errorf("fsblob: delete %s: %w", sha256hex, err)
	}
	return nil
}

// ListMissing возвращает, каких из запрошенных хэшей нет локально.
func (s *Store) ListMissing(ctx context.Context, hashes []string) ([]string, error) {
	var missing []string
	for _, h := range hashes {
		ok, err := s.Has(ctx, h)
		if err != nil {
			return nil, err
		}
		if !ok {
			missing = append(missing, strings.ToLower(h))
		}
	}
	return missing, nil
}

// Size возвращает размер блоба в байтах.
func (s *Store) Size(_ context.Context, sha256hex string) (int64, error) {
	p, err := s.path(sha256hex)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(p)
	if os.IsNotExist(err) {
		return 0, fmt.Errorf("fsblob: size %s: %w", sha256hex, storage.ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("fsblob: size %s: %w", sha256hex, err)
	}
	return info.Size(), nil
}

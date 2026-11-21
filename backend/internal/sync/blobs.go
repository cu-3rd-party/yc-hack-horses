package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
)

// syncBlobs — неполный синк по потребности: качаем с пира только blob'ы, на
// которые ссылается эта нода (наблюдения), но которых нет на диске. Датасет
// может быть на порядки больше, чем наблюдений (ADR-0009), поэтому целиком
// реплицировать его нельзя: недостающие доливаются постепенно, не больше
// cfg.MaxBlobsPerSync за цикл. Blob, которого нет и у пира, пропускается —
// он останется в списке недостающих до следующего цикла.
func (e *Engine) syncBlobs(ctx context.Context, peer Peer) error {
	if e.blobs == nil || e.cfg.NeededBlobHashes == nil {
		return nil
	}
	needed, err := e.cfg.NeededBlobHashes(ctx)
	if err != nil {
		return fmt.Errorf("list needed blob hashes: %w", err)
	}
	// Legacy-ссылки на blob UUID в наблюдениях через content-addressed канал
	// не едут — фильтруем до ListMissing, иначе он падает на невалидном хэше.
	var valid []string
	for _, h := range needed {
		if storage.ValidSha256Hex(h) {
			valid = append(valid, h)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	missing, err := e.blobs.ListMissing(ctx, valid)
	if err != nil {
		return fmt.Errorf("list missing blobs: %w", err)
	}
	if len(missing) > e.cfg.maxBlobsPerSync() {
		missing = missing[:e.cfg.maxBlobsPerSync()]
	}
	for _, h := range missing {
		if err := e.DownloadBlob(ctx, peer, h); err != nil {
			// Пир может не иметь блоба — неполный синк допустим, не срываем цикл.
			e.log.Warn("blob sync: пропуск недостающего blob", "sha256", h, "err", err)
		}
	}
	return nil
}

// DownloadBlob принимает blob от пира чанками и пишет его в BlobStore,
// сверив sha256 после приёма. Несовпадение — ошибка без записи.
func (e *Engine) DownloadBlob(ctx context.Context, peer Peer, sha256hex string) error {
	r, err := peer.DownloadBlob(ctx, sha256hex)
	if err != nil {
		return fmt.Errorf("download blob %s: %w", sha256hex, err)
	}
	defer r.Close() //nolint:errcheck // чтение уже завершено
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read blob %s stream: %w", sha256hex, err)
	}
	return e.storeBlob(ctx, sha256hex, data)
}

// storeBlob проверяет хэш и сохраняет; повторная запись того же хэша — no-op.
func (e *Engine) storeBlob(ctx context.Context, sha256hex string, data []byte) error {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != sha256hex {
		return fmt.Errorf("%w: want %s got %s", ErrBlobHash, sha256hex, got)
	}
	has, err := e.blobs.Has(ctx, sha256hex)
	if err != nil {
		return fmt.Errorf("check blob %s: %w", sha256hex, err)
	}
	if has {
		return nil // дедупликация: blob уже записан
	}
	if err := e.blobs.Put(ctx, sha256hex, "", data); err != nil {
		return fmt.Errorf("store blob %s: %w", sha256hex, err)
	}
	return nil
}

package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// syncBlobs доводит blobs до обеих сторон: отдаём пиру те, которых у него нет,
// и забираем те, которых нет у нас (пир перечисляет недостающее через наш же
// вызов RequestMissingBlobs при его цикле Sync — протокол симметричный).
func (e *Engine) syncBlobs(ctx context.Context, peer Peer) error {
	if e.blobs == nil || e.cfg.KnownBlobHashes == nil {
		return nil
	}
	hashes, err := e.cfg.KnownBlobHashes(ctx)
	if err != nil {
		return fmt.Errorf("list known blob hashes: %w", err)
	}
	if len(hashes) == 0 {
		return nil
	}
	missingAtPeer, err := peer.RequestMissingBlobs(ctx, hashes)
	if err != nil {
		return fmt.Errorf("query missing blobs at peer: %w", err)
	}
	for _, h := range missingAtPeer {
		if err := e.uploadBlob(ctx, peer, h); err != nil {
			return err
		}
	}
	// Недостающие у нас: пира просим отдать их через DownloadBlob — но список
	// хэшей пира получаем зеркально: он сам запустит Sync и дольёт нас.
	return nil
}

// uploadBlob читает локальный blob и передаёт его пиру чанками.
// Повторная передача существующего blob — no-op (дедупликация по sha256).
func (e *Engine) uploadBlob(ctx context.Context, peer Peer, sha256hex string) error {
	data, err := e.blobs.Get(ctx, sha256hex)
	if err != nil {
		return fmt.Errorf("read blob %s: %w", sha256hex, err)
	}
	if err := peer.UploadBlob(ctx, sha256hex, newChunkReader(data, e.cfg.chunkSize())); err != nil {
		return fmt.Errorf("upload blob %s: %w", sha256hex, err)
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

// PullMissingBlobs запрашивает у пира blob'ы, которых нет локально.
// Кандидаты — хэши, известные этой ноде; пир отдаёт каждый через DownloadBlob.
func (e *Engine) PullMissingBlobs(ctx context.Context, peer Peer, candidates []string) error {
	missing, err := e.blobs.ListMissing(ctx, candidates)
	if err != nil {
		return fmt.Errorf("list missing blobs: %w", err)
	}
	for _, h := range missing {
		if err := e.DownloadBlob(ctx, peer, h); err != nil {
			return err
		}
	}
	return nil
}

// chunkReader отдаёт фиксированный буфер чанками — io.Reader, чанками по chunkSize.
type chunkReader struct {
	data   []byte
	chunk  int
	offset int
}

func newChunkReader(data []byte, chunk int) *chunkReader {
	return &chunkReader{data: data, chunk: chunk}
}

// Read возвращает не больше c.chunk байт за вызов — каждый вызов = один чанк
// с offset = позиция в данных.
func (c *chunkReader) Read(p []byte) (int, error) {
	if c.offset >= len(c.data) {
		return 0, io.EOF
	}
	n := min(c.chunk, len(p))
	n = copy(p[:n], c.data[c.offset:min(c.offset+n, len(c.data))])
	c.offset += n
	if c.offset >= len(c.data) {
		return n, io.EOF
	}
	return n, nil
}

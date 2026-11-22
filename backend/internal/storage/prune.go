package storage

import (
	"context"
	"errors"
	"log/slog"
)

// BlobReferencer сообщает, ссылается ли на блоб хоть одно наблюдение.
// Реализуется sqlstore.Store; интерфейс разрывает цикл storage <- sqlstore.
type BlobReferencer interface {
	BlobReferenced(ctx context.Context, sha256hex string) (bool, error)
}

// PruneBlobs удаляет физические файлы мягко удалённых блобов, на которые больше
// не ссылается ни одно наблюдение. Вызывается ПОСЛЕ коммита проекции blob.deleted:
// внутри транзакции файл удалять нельзя — откат не вернёт данные.
// Ошибки не фатальны для события (метаданные уже помечены): они логируются,
// файл останется на диске до следующей попытки.
func PruneBlobs(ctx context.Context, refs BlobReferencer, blobs BlobStore, shas []string, log *slog.Logger) {
	if blobs == nil || refs == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	for _, sha := range shas {
		if !ValidSha256Hex(sha) {
			continue
		}
		referenced, err := refs.BlobReferenced(ctx, sha)
		if err != nil {
			log.Warn("blob prune: reference check failed", "sha256", sha, "err", err)
			continue
		}
		if referenced {
			log.Debug("blob prune: still referenced by observations", "sha256", sha)
			continue
		}
		if err := blobs.Delete(ctx, sha); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue // файла уже нет — удаление идемпотентно
			}
			log.Warn("blob prune: delete failed", "sha256", sha, "err", err)
			continue
		}
		log.Info("blob file removed", "sha256", sha)
	}
}

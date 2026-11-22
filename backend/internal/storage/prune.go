package storage

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

// BlobReferencer сообщает, ссылается ли на блоб хоть одно наблюдение.
// Реализуется sqlstore.Store; интерфейс разрывает цикл storage <- sqlstore.
type BlobReferencer interface {
	BlobReferenced(ctx context.Context, sha256hex string) (bool, error)
}

// DeletedBlobLister перечисляет sha256 блобов, помеченных мягко удалёнными
// (deleted_at != 0) и ещё не обработанных рипером (pruned_at). Нужен фоновому
// риперу: он не знает, какое именно событие могло оставить файл на диске.
// Реализуется sqlstore.Store (ListDeletedBlobs), SQL в контрактный пакет не течёт.
type DeletedBlobLister interface {
	ListDeletedBlobs(ctx context.Context) ([]string, error)
}

// BlobDeletionChecker сообщает, помечен ли блоб удалённым НА МОМЕНТ вызова.
// Реализуется sqlstore.Store (метод BlobDeleted); интерфейс разрывает цикл
// storage <- sqlstore. Нужен, чтобы поймать гонку ре-аплоада: между выборкой
// удалённых блобов и физическим удалением блоб мог снова стать активным.
// PruneBlobs сам обнаруживает эту реализацию у refs и оборачивает её в
// revivedGuard — проверка deleted_at работает для всех путей, а не только рипера.
type BlobDeletionChecker interface {
	BlobDeleted(ctx context.Context, idOrSHA string) (time.Time, bool, error)
}

// BlobPruneMarker помечает блоб обработанным рипером (pruned_at). Реализуется
// sqlstore.Store. Опционален: PruneBlobs вызывает его, только если переданная
// реализация BlobReferencer умеет это делать — сигнатура PruneBlobs остаётся
// совместимой с вызовами из HTTP-хендлера.
type BlobPruneMarker interface {
	MarkBlobPruned(ctx context.Context, sha256hex string, prunedAt time.Time) error
}

// BlobReaperInterval — фиксированный период фонового рипера.
const BlobReaperInterval = 30 * time.Minute

// BlobReaperTimeout — верхняя граница одного прохода рипера.
const BlobReaperTimeout = 5 * time.Minute

// PruneBlobs удаляет физические файлы мягко удалённых блобов, на которые больше
// не ссылается ни одно наблюдение. Вызывается ПОСЛЕ коммита проекции blob.deleted:
// внутри транзакции файл удалять нельзя — откат не вернёт данные.
// Ошибки не фатальны для события (метаданные уже помечены): они логируются,
// файл останется на диске до следующей попытки.
//
// Если refs реализует BlobDeletionChecker, здесь же включается проверка
// deleted_at (revivedGuard): так её получают все три пути — HTTP-хендлер
// удаления, проекция события от пира и фоновый рипер. Раньше обёртка жила в
// ReapDeletedBlobs, и HTTP/проекция оставались без этой проверки.
//
// ВАЖНО: вызов из HTTP-хендлера (transport/http/handlers.go) передаёт sha,
// попавший в prune-список только если ProjectBlobDeleted вернул rows-affected
// > 0. Этот gate чинится в ядре (loop-1); без него хендлер мог бы отдать на
// удаление sha устаревшего/повторного удаления — тогда последней линией защиты
// остаётся исключительно revivedGuard ниже.
func PruneBlobs(ctx context.Context, refs BlobReferencer, blobs BlobStore, shas []string, log *slog.Logger) {
	if blobs == nil || refs == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	// Одна строка-обёртка: если реализация умеет сообщать deleted_at, файл не
	// удаляется, пока строка снова не станет активной (ре-аплоад).
	if checker, ok := refs.(BlobDeletionChecker); ok {
		refs = revivedGuard{refs: refs, checker: checker}
	}
	// Первый проход группируем: одна пакетная проверка ссылок (и удалённости)
	// на группу блобов вместо двух сканов на каждый блоб. Отсевшие кандидаты
	// (редкие) проходят полный двухпроходный путь ниже; это безопасно, потому
	// что пакетный ответ консервативен: «ссылается» с любым сомнением.
	if batcher, ok := refs.(interface {
		BlobReferencedBatch(ctx context.Context, shas []string) (map[string]bool, error)
	}); ok && len(shas) > 1 {
		if deleter, ok := refs.(interface {
			BlobsDeleted(ctx context.Context, shas []string) (map[string]bool, error)
		}); ok {
			shas = batchFirstPass(ctx, batcher, deleter, refs, shas, log)
			if len(shas) == 0 {
				return
			}
		}
	}
	for _, sha := range shas {
		if !ValidSha256Hex(sha) {
			continue
		}
		ok, err := canDeleteBlob(ctx, refs, sha)
		if err != nil {
			log.Warn("blob prune: reference check failed", "sha256", sha, "err", err)
			continue
		}
		if !ok {
			log.Debug("blob prune: still referenced by observations", "sha256", sha)
			continue
		}
		// Перепроверка непосредственно перед удалением. Остаточное окно между
		// ней и blobs.Delete честно существует: аплоад пишет файл на диск ДО
		// UpsertBlob (регистрация строки идёт после записи файла), поэтому
		// ожившую строку видно лишь спустя время после появления нового файла.
		// Радикальную перестановку порядка в upload (UpsertBlob до файла) здесь
		// НЕ делаем — это чужой код, отдельная правка. Выбранное смягчение:
		// (1) перепроверка сужает окно до одного вызова Delete; (2) MarkBlobPruned
		// (см. reaper.go) не помечает блоб, если строку успели оживить
		// (deleted_at <> 0 в WHERE), так что ре-аплоад в этом окне не теряет
		// pruned_at и файл перезапишется при следующем аплоаде. Физически удалить
		// свежезалитый файл в этом окне всё же можно; цена — повторный аплоад.
		//
		// Стоимость: при активном revivedGuard каждый canDeleteBlob делает
		// 2 запроса (BlobDeleted + BlobReferenced), то есть блоб сканируется
		// дважды — 2×BlobDeleted + 2×BlobReferenced. Кэшировать вердикт первой
		// проверки нельзя: именно свежее повторное чтение ловит ре-аплоад в окне
		// между проверкой и Delete, а кэш превратил бы перепроверку в no-op.
		ok, err = canDeleteBlob(ctx, refs, sha)
		if err != nil {
			log.Warn("blob prune: recheck before delete failed", "sha256", sha, "err", err)
			continue
		}
		if !ok {
			log.Info("blob prune: blob revived before delete", "sha256", sha)
			continue
		}
		err = blobs.Delete(ctx, sha)
		switch {
		case err == nil:
			log.Info("blob file removed", "sha256", sha)
		case errors.Is(err, ErrNotFound):
			// файла уже нет — удаление идемпотентно
			log.Debug("blob prune: file already absent", "sha256", sha)
		default:
			log.Warn("blob prune: delete failed", "sha256", sha, "err", err)
			continue
		}
		markBlobPruned(ctx, refs, sha, log)
	}
}

// canDeleteBlob — единая точка проверки «блоб ещё удалён и ни на что не ссылается».
func canDeleteBlob(ctx context.Context, refs BlobReferencer, sha string) (bool, error) {
	referenced, err := refs.BlobReferenced(ctx, sha)
	if err != nil {
		return false, err
	}
	return !referenced, nil
}

// markBlobPruned помечает блоб обработанным, если реализация это умеет.
// Неудачная отметка не ошибка прохода: файл уже удалён, следующий проход
// получит ErrNotFound и повторит отметку.
func markBlobPruned(ctx context.Context, refs BlobReferencer, sha string, log *slog.Logger) {
	marker, ok := refs.(BlobPruneMarker)
	if !ok {
		return
	}
	if err := marker.MarkBlobPruned(ctx, sha, time.Now().UTC()); err != nil {
		log.Warn("blob prune: mark pruned failed", "sha256", sha, "err", err)
	}
}

// revivedGuard — обёртка над BlobReferencer, которая дополнительно блокирует
// удаление блоба, снова ставшего активным (deleted_at = 0). Ре-аплоаднутый блоб
// какое-то время может не иметь наблюдений, поэтому одной проверки ссылок мало:
// если блоб не помечен удалённым — считаем, что ссылка есть, и файл не трогаем.
type revivedGuard struct {
	refs    BlobReferencer
	checker BlobDeletionChecker
}

func (g revivedGuard) BlobReferenced(ctx context.Context, sha string) (bool, error) {
	if g.checker != nil {
		_, deleted, err := g.checker.BlobDeleted(ctx, sha)
		if err != nil {
			return false, err
		}
		if !deleted {
			return true, nil // блоб снова активен — не удаляем
		}
	}
	return g.refs.BlobReferenced(ctx, sha)
}

// MarkBlobPruned пробрасывает отметку во внутреннюю реализацию, если она умеет.
func (g revivedGuard) MarkBlobPruned(ctx context.Context, sha string, prunedAt time.Time) error {
	marker, ok := g.refs.(BlobPruneMarker)
	if !ok {
		return nil
	}
	return marker.MarkBlobPruned(ctx, sha, prunedAt)
}

// BlobReferencedBatch пробрасывает пакетную проверку ссылок во внутреннюю
// реализацию (revivedGuard сам по себе ссылки не считает).
func (g revivedGuard) BlobReferencedBatch(ctx context.Context, shas []string) (map[string]bool, error) {
	batcher, ok := g.refs.(interface {
		BlobReferencedBatch(ctx context.Context, shas []string) (map[string]bool, error)
	})
	if !ok {
		return nil, errors.New("blob references batch: not supported")
	}
	return batcher.BlobReferencedBatch(ctx, shas)
}

// BlobsDeleted пробрасывает пакетную проверку удалённости во внутреннюю
// реализацию; без неё — пособытийно через checker (медленно, но корректно).
func (g revivedGuard) BlobsDeleted(ctx context.Context, shas []string) (map[string]bool, error) {
	if batcher, ok := g.refs.(interface {
		BlobsDeleted(ctx context.Context, shas []string) (map[string]bool, error)
	}); ok {
		return batcher.BlobsDeleted(ctx, shas)
	}
	out := map[string]bool{}
	for _, sha := range shas {
		_, deleted, err := g.checker.BlobDeleted(ctx, sha)
		if err != nil {
			return nil, err
		}
		out[strings.ToLower(sha)] = deleted
	}
	return out, nil
}

// ReapDeletedBlobs — один проход рипера: находит все мягко удалённые блобы и
// прогоняет их через тот же PruneBlobs. Идемпотентно: отсутствие файла — не
// ошибка. Закрывает дыру, когда нода упала между коммитом проекции blob.deleted
// и физическим удалением файла: повторного события не будет — журнал
// синхронизации дедуплицируется по (node_id, sequence), т.е. файл иначе
// остался бы на диске навсегда. Обработанные блобы помечаются pruned_at и
// больше не попадают в выборку.
// Проверку deleted_at включает сам PruneBlobs (revivedGuard), если refs её
// реализует: отдельный параметр-checker больше не нужен.
func ReapDeletedBlobs(ctx context.Context, lister DeletedBlobLister, refs BlobReferencer, blobs BlobStore, log *slog.Logger) {
	if lister == nil || refs == nil || blobs == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	shas, err := lister.ListDeletedBlobs(ctx)
	if err != nil {
		log.Warn("blob reaper: list deleted blobs failed", "err", err)
		return
	}
	if len(shas) == 0 {
		return
	}
	log.Debug("blob reaper: scanning deleted blobs", "count", len(shas))
	PruneBlobs(ctx, refs, blobs, shas, log)
}

// RunBlobReaper крутит рипер до отмены ctx: немедленный проход, затем каждые
// BlobReaperInterval. Вызывается фоновой горутиной ноды. Каждый проход
// ограничен BlobReaperTimeout, чтобы не висеть на медленном хранилище.
// Необязательный tick подменяет тикер (тесты); при nil-канале тики не приходят,
// а закрытие канала завершает цикл (иначе был бы busy-loop на нулевых значениях).
func RunBlobReaper(ctx context.Context, lister DeletedBlobLister, refs BlobReferencer, blobs BlobStore, log *slog.Logger, tick ...<-chan time.Time) {
	if log == nil {
		log = slog.Default()
	}
	runOnce := func() {
		// Паника в одном проходе не должна ронять всю фоновую горутину: логируем
		// и продолжаем ждать следующий тик.
		defer func() {
			if r := recover(); r != nil {
				log.Error("blob reaper: panic in pass", "panic", r)
			}
		}()
		passCtx, cancel := context.WithTimeout(ctx, BlobReaperTimeout)
		defer cancel()
		ReapDeletedBlobs(passCtx, lister, refs, blobs, log)
	}
	var ticks <-chan time.Time
	if len(tick) > 0 && tick[0] != nil {
		ticks = tick[0]
	} else {
		ticker := time.NewTicker(BlobReaperInterval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	// Контекст мог быть отменён до запуска — немедленный проход не нужен.
	if ctx.Err() != nil {
		return
	}
	runOnce()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ticks:
			if !ok {
				return
			}
			runOnce()
		}
	}
}

// batchFirstPass — групповой отсев перед полным двухпроходным путём: блобы,
// на которые ссылается хоть одно наблюдение (или снова активные после
// ре-аплоада), выкидываются из списка. Ответ пакета консервативен: «ссылается»
// при любом сомнении, поэтому безопаснее per-sha проверок, а не строже.
// Ошибка пакетной проверки не фатальна: исходный список сохраняется, все блобы
// пройдут полный путь с per-sha проверками.
func batchFirstPass(
	ctx context.Context,
	batcher interface {
		BlobReferencedBatch(ctx context.Context, shas []string) (map[string]bool, error)
	},
	deleter interface {
		BlobsDeleted(ctx context.Context, shas []string) (map[string]bool, error)
	},
	refs BlobReferencer,
	shas []string,
	log *slog.Logger,
) []string {
	_ = refs
	var valid []string
	for _, sha := range shas {
		if ValidSha256Hex(sha) {
			valid = append(valid, sha)
		}
	}
	referenced, err := batcher.BlobReferencedBatch(ctx, valid)
	if err != nil {
		log.Warn("blob prune: batch reference check failed, falling back to per-blob checks", "err", err)
		return shas
	}
	deleted, err := deleter.BlobsDeleted(ctx, valid)
	if err != nil {
		log.Warn("blob prune: batch deletion check failed, falling back to per-blob checks", "err", err)
		return shas
	}
	out := make([]string, 0, len(shas))
	for _, sha := range shas {
		if !ValidSha256Hex(sha) {
			continue
		}
		key := strings.ToLower(sha)
		if referenced[key] {
			log.Debug("blob prune: still referenced by observations", "sha256", sha)
			continue
		}
		if !deleted[key] {
			// блоб снова активен — не удаляем (revivedGuard в per-sha пути дал бы то же)
			log.Debug("blob prune: blob revived before delete", "sha256", sha)
			continue
		}
		out = append(out, sha)
	}
	return out
}

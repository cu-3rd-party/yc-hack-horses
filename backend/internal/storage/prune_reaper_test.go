package storage_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/fsblob"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlite"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
)

// newReaperFixture поднимает sqlite + fsblob во временном каталоге.
// Путь к migrations/ относителен каталога пакета (internal/storage).
func newReaperFixture(t *testing.T) (*sqlstore.Store, *fsblob.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := migrate.Apply(context.Background(), st.DB(), "../../migrations", "sqlite"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	blobs, err := fsblob.New(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("fsblob.New: %v", err)
	}
	return st, blobs
}

// putBlob кладёт файл на диск и регистрирует метаданные блоба.
func putBlob(t *testing.T, st *sqlstore.Store, blobs *fsblob.Store, content []byte) string {
	t.Helper()
	ctx := context.Background()
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	if err := blobs.Put(ctx, sha, "image/jpeg", content); err != nil {
		t.Fatalf("blobs.Put: %v", err)
	}
	if _, err := st.UpsertBlob(ctx, domain.Blob{ID: sha, SHA256: sha, MimeType: "image/jpeg", Size: int64(len(content))}); err != nil {
		t.Fatalf("UpsertBlob: %v", err)
	}
	return sha
}

func mustMarkDeleted(t *testing.T, st *sqlstore.Store, sha string) {
	t.Helper()
	if _, err := st.MarkBlobDeleted(context.Background(), sha, time.Now().UTC(), "user"); err != nil {
		t.Fatalf("MarkBlobDeleted: %v", err)
	}
}

// TestBlobReaperRemovesOrphanedFile — крэш между коммитом проекции blob.deleted
// и физическим удалением файла: строка помечена удалённой, но файл ещё на диске.
// Рипер находит её и удаляет файл (повторного события не будет — журнал
// синхронизации дедуплицируется по node_id+sequence).
func TestBlobReaperRemovesOrphanedFile(t *testing.T) {
	ctx := context.Background()
	st, blobs := newReaperFixture(t)
	sha := putBlob(t, st, blobs, []byte("crash-victim-payload"))
	mustMarkDeleted(t, st, sha)

	if ok, err := blobs.Has(ctx, sha); err != nil || !ok {
		t.Fatalf("файл должен остаться на диске до рипера (has=%v err=%v)", ok, err)
	}

	storage.ReapDeletedBlobs(ctx, st, st, blobs, slog.Default())

	if ok, _ := blobs.Has(ctx, sha); ok {
		t.Fatal("рипер должен удалить осиротевший файл удалённого блоба")
	}
}

// TestBlobReaperKeepsActiveBlob — активный (не удалённый) блоб рипер не трогает:
// выборка deleted_at != 0 его не видит, файл остаётся на диске.
func TestBlobReaperKeepsActiveBlob(t *testing.T) {
	ctx := context.Background()
	st, blobs := newReaperFixture(t)
	sha := putBlob(t, st, blobs, []byte("active-payload"))

	storage.ReapDeletedBlobs(ctx, st, st, blobs, slog.Default())

	if ok, _ := blobs.Has(ctx, sha); !ok {
		t.Fatal("активный блоб не должен удаляться рипером")
	}
}

// TestBlobReaperKeepsReferencedBlob — на блоб ссылается наблюдение: даже после
// мягкого удаления строки файл удалять нельзя.
func TestBlobReaperKeepsReferencedBlob(t *testing.T) {
	ctx := context.Background()
	st, blobs := newReaperFixture(t)
	sha := putBlob(t, st, blobs, []byte("referenced-payload"))

	if err := st.CreateObservation(ctx, domain.Observation{
		ID:         "obs-1",
		NodeID:     "node-1",
		ObservedAt: time.Now().UTC(),
		ObserverID: "user-1",
		BlobIDs:    []string{sha},
	}); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	mustMarkDeleted(t, st, sha)

	storage.ReapDeletedBlobs(ctx, st, st, blobs, slog.Default())

	if ok, _ := blobs.Has(ctx, sha); !ok {
		t.Fatal("файл блоба, на который ссылается наблюдение, удалять нельзя")
	}
}

// TestBlobReaperKeepsRevivedBlob — гонка с ре-аплоадом: между выборкой удалённых
// и удалением файла блоб снова стал активным (deleted_at = 0). Список приходит
// извне (как будто SELECT отработал до ре-аплоада), но рипер обязан заметить,
// что блоб уже не удалён, и не удалять файл. Путь рипера: guard внутри PruneBlobs.
func TestBlobReaperKeepsRevivedBlob(t *testing.T) {
	ctx := context.Background()
	st, blobs := newReaperFixture(t)
	sha := putBlob(t, st, blobs, []byte("revived-payload"))

	mustMarkDeleted(t, st, sha)
	// Ре-аплоад: deleted_at возвращается в 0, файл на месте.
	if _, err := st.UpsertBlob(ctx, domain.Blob{ID: sha, SHA256: sha, MimeType: "image/jpeg", Size: 15}); err != nil {
		t.Fatalf("UpsertBlob (revive): %v", err)
	}

	// Листер отдаёт устаревший список (sha в нём), имитируя окно гонки.
	storage.ReapDeletedBlobs(ctx, staleLister{shas: []string{sha}}, st, blobs, slog.Default())

	if ok, _ := blobs.Has(ctx, sha); !ok {
		t.Fatal("ре-аплоаднутый блоб не должен быть удалён рипером")
	}
}

// TestPruneBlobsKeepsRevivedBlobDirectPath — тот же ре-аплоад, но проверяется
// прямой путь PruneBlobs, то есть HTTP-хендлер удаления и проекция события от
// пира. Guard теперь включается внутри PruneBlobs, поэтому эти пути тоже
// получают проверку deleted_at: файл оживлённого блоба не трогаем.
func TestPruneBlobsKeepsRevivedBlobDirectPath(t *testing.T) {
	ctx := context.Background()
	st, blobs := newReaperFixture(t)
	sha := putBlob(t, st, blobs, []byte("revived-direct"))
	mustMarkDeleted(t, st, sha)
	// Ре-аплоад: строка снова активна, а список пришёл из устаревшего события.
	if _, err := st.UpsertBlob(ctx, domain.Blob{ID: sha, SHA256: sha, MimeType: "image/jpeg", Size: 13}); err != nil {
		t.Fatalf("UpsertBlob (revive): %v", err)
	}

	// Прямой вызов, как из handlers.go / projectedStore.Append.
	storage.PruneBlobs(ctx, st, blobs, []string{sha}, slog.Default())

	if ok, _ := blobs.Has(ctx, sha); !ok {
		t.Fatal("прямой путь (HTTP/проекция) не должен удалять ре-аплоаднутый блоб")
	}
}

// TestBlobReaperKeepsBlobRevivedDuringPass — сценарий TOCTOU: строка ещё
// удалена на первой проверке, но между проверкой и удалением аплоад дописал
// файл и оживил строку. Guard перечитывает deleted_at перед blobs.Delete и
// файл не трогает.
func TestBlobReaperKeepsBlobRevivedDuringPass(t *testing.T) {
	ctx := context.Background()
	st, blobs := newReaperFixture(t)
	sha := putBlob(t, st, blobs, []byte("toctou-payload"))
	mustMarkDeleted(t, st, sha)

	// После первой проверки ссылок (ещё «не ссылается, удалён») имитируем
	// ре-аплоад: файл уже записан, строка оживает.
	refs := &hookRefs{
		delegate: st,
		checker:  st,
		after: func(call int) {
			if call != 1 {
				return
			}
			if _, err := st.UpsertBlob(ctx, domain.Blob{ID: sha, SHA256: sha, MimeType: "image/jpeg", Size: 14}); err != nil {
				t.Errorf("UpsertBlob (revive during pass): %v", err)
			}
		},
	}
	storage.ReapDeletedBlobs(ctx, staleLister{shas: []string{sha}}, refs, blobs, slog.Default())

	if ok, _ := blobs.Has(ctx, sha); !ok {
		t.Fatal("guard должен перепроверить deleted_at перед удалением и сохранить файл")
	}
}

// TestPruneBlobsRechecksBeforeDelete — контрактная проверка: PruneBlobs
// перечитывает ссылки непосредственно перед удалением и не вызывает Delete,
// если на втором чтении блоб снова стал недоступен для удаления.
func TestPruneBlobsRechecksBeforeDelete(t *testing.T) {
	ctx := context.Background()
	st, blobs := newReaperFixture(t)
	sha := putBlob(t, st, blobs, []byte("recheck-payload"))
	mustMarkDeleted(t, st, sha)

	refs := &hookRefs{answers: []bool{false, true}}
	counting := &countingBlobs{Store: blobs}
	storage.PruneBlobs(ctx, refs, counting, []string{sha}, slog.Default())

	if counting.deletes != 0 {
		t.Fatalf("Delete не должен вызываться после успешной перепроверки: %d", counting.deletes)
	}
	if ok, _ := blobs.Has(ctx, sha); !ok {
		t.Fatal("файл должен остаться на диске")
	}
}

// TestBlobReaperMarksPrunedAndIdempotent — после удаления (или если файла уже
// нет) блоб помечается pruned_at и выпадает из выборки: повторный проход не
// перебирает его снова. Повторный прогон идемпотентен.
func TestBlobReaperMarksPrunedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	st, blobs := newReaperFixture(t)

	// Первый блоб: файл на диске, удаляется рипером.
	sha1 := putBlob(t, st, blobs, []byte("prune-me"))
	mustMarkDeleted(t, st, sha1)
	// Второй блоб: файл уже отсутствует (крэш после удаления файла).
	sha2 := putBlob(t, st, blobs, []byte("file-already-gone"))
	mustMarkDeleted(t, st, sha2)
	if err := blobs.Delete(ctx, sha2); err != nil {
		t.Fatalf("blobs.Delete (prepare): %v", err)
	}

	storage.ReapDeletedBlobs(ctx, st, st, blobs, slog.Default())
	storage.ReapDeletedBlobs(ctx, st, st, blobs, slog.Default())

	for _, sha := range []string{sha1, sha2} {
		if ok, _ := blobs.Has(ctx, sha); ok {
			t.Fatalf("файл %s должен быть удалён", sha)
		}
		var prunedAt int64
		if err := st.DB().QueryRowContext(ctx,
			`SELECT pruned_at FROM blobs WHERE sha256 = ?`, sha).Scan(&prunedAt); err != nil {
			t.Fatalf("чтение pruned_at: %v", err)
		}
		if prunedAt == 0 {
			t.Fatalf("блоб %s должен быть помечен pruned_at", sha)
		}
	}

	// Обработанные блобы больше не попадают в выборку рипера.
	pending, err := st.ListDeletedBlobs(ctx)
	if err != nil {
		t.Fatalf("ListDeletedBlobs: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("после прохода не должно остаться кандидатов, got %v", pending)
	}
}

// TestMarkBlobPrunedSkipsRevived — гонка между физическим удалением файла и
// отметкой pruned_at: строку успели оживить (ре-аплоад). Отметка не должна
// применяться к активной строке (WHERE deleted_at <> 0), иначе pruned_at от
// прошлого цикла исключил бы блоб из кандидатов и файл осиротел бы навсегда.
func TestMarkBlobPrunedSkipsRevived(t *testing.T) {
	ctx := context.Background()
	st, blobs := newReaperFixture(t)
	sha := putBlob(t, st, blobs, []byte("mark-revived"))
	mustMarkDeleted(t, st, sha)

	// Ре-аплоад до пометки pruned_at (имитирует revive в окне между Delete и mark).
	if _, err := st.UpsertBlob(ctx, domain.Blob{ID: sha, SHA256: sha, MimeType: "image/jpeg", Size: 12}); err != nil {
		t.Fatalf("UpsertBlob (revive): %v", err)
	}
	if err := st.MarkBlobPruned(ctx, sha, time.Now().UTC()); err != nil {
		t.Fatalf("MarkBlobPruned: %v", err)
	}

	var prunedAt, deletedAt int64
	if err := st.DB().QueryRowContext(ctx,
		`SELECT pruned_at, deleted_at FROM blobs WHERE sha256 = ?`, sha).Scan(&prunedAt, &deletedAt); err != nil {
		t.Fatalf("чтение pruned_at: %v", err)
	}
	if deletedAt != 0 {
		t.Fatalf("блоб должен быть активен после ре-аплоада, deleted_at=%d", deletedAt)
	}
	if prunedAt != 0 {
		t.Fatalf("активную строку помечать pruned_at нельзя, got %d", prunedAt)
	}
}

// TestBlobReaperRepicksAfterReviveAndSecondDelete — повторное удаление после
// ре-аплоада снова даёт кандидата. UpsertBlob на оживлении сбрасывает pruned_at
// в 0, поэтому Sleep между удалениями не нужен: выборка (pruned_at = 0 OR
// pruned_at < deleted_at) ловит строку сразу по pruned_at = 0.
func TestBlobReaperRepicksAfterReviveAndSecondDelete(t *testing.T) {
	ctx := context.Background()
	st, blobs := newReaperFixture(t)
	sha := putBlob(t, st, blobs, []byte("second-delete"))
	mustMarkDeleted(t, st, sha)
	storage.ReapDeletedBlobs(ctx, st, st, blobs, slog.Default())

	// Ре-аплоад с тем же содержимым и повторное удаление в тот же тик времени.
	if err := blobs.Put(ctx, sha, "image/jpeg", []byte("second-delete")); err != nil {
		t.Fatalf("blobs.Put (revive): %v", err)
	}
	if _, err := st.UpsertBlob(ctx, domain.Blob{ID: sha, SHA256: sha, MimeType: "image/jpeg", Size: 13}); err != nil {
		t.Fatalf("UpsertBlob (revive): %v", err)
	}
	mustMarkDeleted(t, st, sha)

	pending, err := st.ListDeletedBlobs(ctx)
	if err != nil {
		t.Fatalf("ListDeletedBlobs: %v", err)
	}
	if len(pending) != 1 || pending[0] != sha {
		t.Fatalf("после повторного удаления блоб снова кандидат, got %v", pending)
	}
	storage.ReapDeletedBlobs(ctx, st, st, blobs, slog.Default())
	if ok, _ := blobs.Has(ctx, sha); ok {
		t.Fatal("повторно удалённый блоб должен быть вычищен")
	}
}

// TestRunBlobReaperImmediatePassAndTicks — рипер делает немедленный проход,
// затем проход по тику, и завершается по отмене ctx.
func TestRunBlobReaperImmediatePassAndTicks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, blobs := newReaperFixture(t)

	lister := &countingLister{delegate: st}
	ticks := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		storage.RunBlobReaper(ctx, lister, st, blobs, slog.Default(), ticks)
	}()

	waitFor(t, func() bool { return lister.count() == 1 }, "немедленный проход")
	ticks <- time.Now()
	waitFor(t, func() bool { return lister.count() == 2 }, "проход по тику")
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunBlobReaper не завершился по отмене ctx")
	}
}

// TestRunBlobReaperStopsOnClosedTick — закрытие tick-канала завершает цикл
// (иначе select вечно читал бы нулевые значения и крутил busy-loop).
func TestRunBlobReaperStopsOnClosedTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, blobs := newReaperFixture(t)

	lister := &countingLister{delegate: st}
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		defer close(done)
		storage.RunBlobReaper(ctx, lister, st, blobs, slog.Default(), ticks)
	}()

	waitFor(t, func() bool { return lister.count() == 1 }, "немедленный проход")
	close(ticks)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunBlobReaper должен завершиться по закрытию tick-канала")
	}
}

// TestRunBlobReaperSurvivesPassPanic — паника в одном проходе не роняет цикл:
// runOnce ловит её через recover, горутина продолжает ждать тики и делает
// следующий проход.
func TestRunBlobReaperSurvivesPassPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, blobs := newReaperFixture(t)

	lister := &panicLister{}
	ticks := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		storage.RunBlobReaper(ctx, lister, st, blobs, slog.Default(), ticks)
	}()

	// Немедленный проход паникует, но цикл это переживает.
	waitFor(t, func() bool { return lister.count() == 1 }, "первый проход (с паникой)")
	ticks <- time.Now()
	waitFor(t, func() bool { return lister.count() == 2 }, "второй проход после паники")

	// Горутина всё ещё жива: завершаем её по отмене ctx.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunBlobReaper не завершился по отмене ctx после паники в проходе")
	}
}

// TestRunBlobReaperReturnsEarlyOnCancelledCtx — если контекст уже отменён до
// запуска, рипер не делает немедленный проход и сразу возвращается.
func TestRunBlobReaperReturnsEarlyOnCancelledCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // отменяем до запуска
	st, blobs := newReaperFixture(t)

	lister := &countingLister{delegate: st}
	done := make(chan struct{})
	go func() {
		defer close(done)
		storage.RunBlobReaper(ctx, lister, st, blobs, slog.Default(), make(chan time.Time))
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunBlobReaper должен сразу завершиться при уже отменённом ctx")
	}
	if n := lister.count(); n != 0 {
		t.Fatalf("при отменённом ctx проходов быть не должно, got %d", n)
	}
}

// TestBlobReaperCleanupNoDoubleWaitDeadlock — контракт cleanup app.Run: после
// stopBackground (отмена контекстов фоновых контуров) идёт doneW.Wait(), и
// повторный Wait не должен дедлочить. Проверяем на уровне sync.WaitGroup,
// повторяющем паттерн doneW (unit-уровень, без поднятия App).
func TestBlobReaperCleanupNoDoubleWaitDeadlock(t *testing.T) {
	var doneW sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	for i := 0; i < 3; i++ {
		doneW.Add(1)
		go func() {
			defer doneW.Done()
			<-ctx.Done()
		}()
	}
	// stopBackground: отменяем контексты фоновых контуров, затем ждём.
	cancel()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		doneW.Wait()
		doneW.Wait() // повторный Wait по doneW не должен висеть (идемпотентен)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("двойной Wait по doneW не должен дедлочить cleanup app.Run")
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("не дождались: %s", what)
}

// staleLister возвращает заранее подготовленный список, имитируя устаревший
// результат SELECT между выборкой и удалением.
type staleLister struct{ shas []string }

func (l staleLister) ListDeletedBlobs(context.Context) ([]string, error) { return l.shas, nil }

// hookRefs — BlobReferencer с подменой ответов и хуком после вызова.
// answers (если задан) отдаёт ответ по номеру вызова, иначе делегирует.
// checker (если задан) отвечает за deleted_at; без него hookRefs считает блоб
// всё ещё удалённым, чтобы тест перепроверки ссылок не упирался в revivedGuard.
type hookRefs struct {
	delegate storage.BlobReferencer
	checker  storage.BlobDeletionChecker
	answers  []bool
	calls    int
	after    func(call int)
}

func (r *hookRefs) BlobReferenced(ctx context.Context, sha string) (bool, error) {
	r.calls++
	var referenced bool
	if len(r.answers) >= r.calls {
		referenced = r.answers[r.calls-1]
	} else if r.delegate != nil {
		v, err := r.delegate.BlobReferenced(ctx, sha)
		if err != nil {
			return false, err
		}
		referenced = v
	}
	if r.after != nil {
		r.after(r.calls)
	}
	return referenced, nil
}

// BlobDeleted делает hookRefs полноценным BlobDeletionChecker: PruneBlobs тогда
// оборачивает его в revivedGuard, и TOCTOU-проверка deleted_at работает.
func (r *hookRefs) BlobDeleted(ctx context.Context, sha string) (time.Time, bool, error) {
	if r.checker != nil {
		return r.checker.BlobDeleted(ctx, sha)
	}
	return time.Now().UTC(), true, nil
}

// MarkBlobPruned пробрасывает отметку делегату, если тот умеет.
func (r *hookRefs) MarkBlobPruned(ctx context.Context, sha string, at time.Time) error {
	if m, ok := r.delegate.(storage.BlobPruneMarker); ok {
		return m.MarkBlobPruned(ctx, sha, at)
	}
	return nil
}

// countingBlobs считает вызовы Delete.
type countingBlobs struct {
	*fsblob.Store
	deletes int
}

func (c *countingBlobs) Delete(ctx context.Context, sha string) error {
	c.deletes++
	return c.Store.Delete(ctx, sha)
}

// countingLister считает проходы рипера.
type countingLister struct {
	delegate storage.DeletedBlobLister
	mu       sync.Mutex
	n        int
}

func (l *countingLister) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

func (l *countingLister) ListDeletedBlobs(ctx context.Context) ([]string, error) {
	l.mu.Lock()
	l.n++
	l.mu.Unlock()
	return l.delegate.ListDeletedBlobs(ctx)
}

// panicLister паникует на каждом ListDeletedBlobs, считая вызовы: проверяет,
// что recover в runOnce не роняет цикл рипера.
type panicLister struct {
	mu sync.Mutex
	n  int
}

func (l *panicLister) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

func (l *panicLister) ListDeletedBlobs(context.Context) ([]string, error) {
	l.mu.Lock()
	l.n++
	l.mu.Unlock()
	panic("boom in reaper pass")
}

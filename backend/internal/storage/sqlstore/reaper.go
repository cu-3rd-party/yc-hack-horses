package sqlstore

import (
	"context"
	"fmt"
	"time"
)

// ListDeletedBlobs возвращает sha256 блобов, мягко удалённых и ещё не
// обработанных рипером. Условие pruned_at < deleted_at покрывает повторное
// удаление после ре-аплоада: pruned_at от прошлого цикла меньше нового
// deleted_at, поэтому строка снова попадает в выборку.
// ORDER BY deleted_at (без sha256) согласован с частичным индексом
// idx_blobs_pruned_at (deleted_at, pruned_at): префикс индекса даёт порядок по
// deleted_at без отдельной сортировки. Порядок в пределах одного deleted_at
// риперу не важен — проход по кандидатам идемпотентен.
func (s *Store) ListDeletedBlobs(ctx context.Context) ([]string, error) {
	rows, err := s.query(ctx,
		`SELECT sha256 FROM blobs
		 WHERE deleted_at <> 0 AND (pruned_at = 0 OR pruned_at < deleted_at)
		 ORDER BY deleted_at`)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: список удалённых блобов: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, fmt.Errorf("sqlstore: чтение sha256 удалённых блобов: %w", err)
		}
		out = append(out, sha)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlstore: итерация по удалённым блобам: %w", err)
	}
	return out, nil
}

// MarkBlobPruned отмечает, что файл блоба физически удалён (или его уже не было):
// рипер больше не перебирает эту строку. Идемпотентно.
//
// WHERE повторяет условие выборки и защищает от гонки: если строку успели
// оживить (deleted_at = 0) до этой отметки, pruned_at НЕ ставится — иначе
// активная (ре-аплоаднутая) строка получила бы метку из прошлого цикла и её
// снова можно было бы преждевременно исключить из кандидатов на удаление.
// pruned_at < deleted_at допускает повторную пометку после нового удаления.
func (s *Store) MarkBlobPruned(ctx context.Context, sha256hex string, prunedAt time.Time) error {
	if _, err := s.exec(ctx,
		`UPDATE blobs SET pruned_at = ?
		 WHERE sha256 = ? AND deleted_at <> 0 AND (pruned_at = 0 OR pruned_at < deleted_at)`,
		prunedAt.UnixMilli(), sha256hex,
	); err != nil {
		return fmt.Errorf("sqlstore: пометка блоба %s обработанным: %w", sha256hex, err)
	}
	return nil
}

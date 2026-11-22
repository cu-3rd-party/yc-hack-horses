-- 007_blob_pruned: отметка о физическом удалении файла блоба.
-- Рипер после успешного удаления (или когда файла уже нет) ставит pruned_at,
-- чтобы не перебирать те же удалённые блобы на каждом проходе. Повторное
-- мягкое удаление после ре-аплоада даёт deleted_at > pruned_at — условие
-- выборки (pruned_at = 0 OR pruned_at < deleted_at) снова берёт такую строку.
ALTER TABLE blobs ADD COLUMN pruned_at BIGINT NOT NULL DEFAULT 0;
-- Частичный индекс под выборку рипера (deleted_at <> 0 AND (pruned_at = 0 OR
-- pruned_at < deleted_at)): покрывающая пара колонок, только удалённые строки.
-- Префикс (deleted_at) обслуживает и ORDER BY deleted_at в ListDeletedBlobs,
-- поэтому отдельная сортировка не нужна (см. sqlstore/reaper.go).
CREATE INDEX idx_blobs_pruned_at ON blobs (deleted_at, pruned_at) WHERE deleted_at <> 0;

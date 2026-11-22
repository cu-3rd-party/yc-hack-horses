-- 004_blob_deleted: мягкое удаление блобов и инвалидация наблюдений.
-- Строка блоба остаётся (на неё могут ссылаться наблюдения и журнал событий),
-- а API перестаёт отдавать блоб. Наблюдения с удалённым фото получают метку
-- invalidated_at и выпадают из сравнения.
ALTER TABLE blobs ADD COLUMN deleted_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE blobs ADD COLUMN delete_reason TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_blobs_deleted_at ON blobs (deleted_at);

ALTER TABLE observations ADD COLUMN invalidated_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE observations ADD COLUMN invalidation_reason TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_observations_invalidated_at ON observations (invalidated_at);

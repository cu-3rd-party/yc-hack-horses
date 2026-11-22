-- 009_perf_indexes: индексы горячих точек чтения.
-- 1) Поиск похожих trait_snapshots по fingerprint среди подтверждённых
--    (ListSimilarTraitSnapshots): составной (fingerprint, confirmed) закрывает
--    и фильтр, и порядок сортировки запроса.
-- 2) Присоединение фотографий к наблюдениям по sha256 (история, инвалидация
--    наблюдений, проверка ссылок при удалении блоба).
--
-- Нормализация sha256 к нижнему регистру (UPDATE blobs SET sha256=LOWER(...))
-- НЕ выполняется: blobs.sha256 имеет UNIQUE (регистрозависимый), а в старых
-- данных возможны пары строк, отличающиеся только регистром — UPDATE уронит
-- миграцию нарушением уникальности. Все пути записи пишут sha256 в нижнем
-- регистре (hex.EncodeToString), а чтение во всех запросах сравнивает через
-- LOWER, поэтому нормализация не нужна для корректности.
CREATE INDEX IF NOT EXISTS idx_trait_snapshots_fingerprint ON trait_snapshots (fingerprint, confirmed);
CREATE INDEX IF NOT EXISTS idx_observation_photos_sha256 ON observation_photos (sha256);

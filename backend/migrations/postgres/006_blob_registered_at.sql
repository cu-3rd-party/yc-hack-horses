-- 005_blob_registered_at: время последней регистрации блоба.
-- Отдельная колонка нужна, чтобы отличить ре-аплоад после мягкого удаления
-- (blob.registered оживляет строку) от повторной доставки старого blob.deleted:
-- удаление, которое старше последней регистрации, не применяется, иначе ретрай
-- sync убивал бы только что восстановленный блоб.
ALTER TABLE blobs ADD COLUMN registered_at BIGINT NOT NULL DEFAULT 0;

-- Бэкфилл из журнала: время последней регистрации блоба — MAX(occurred_at) его
-- события blob.registered (aggregate_id = sha256). Это защищает блобы,
-- перезалитые до миграции: registered_at получает время ре-аплоада, а не
-- created_at, и старое delete-событие (из окна до ре-аплоада) не применяется.
-- Если события в журнале нет, берём created_at: он всегда предшествует удалению.
UPDATE blobs SET registered_at = COALESCE(
    (SELECT MAX(occurred_at) FROM events
     WHERE event_type = 'blob.registered' AND LOWER(aggregate_id) = LOWER(blobs.sha256)),
    created_at)
WHERE registered_at = 0;

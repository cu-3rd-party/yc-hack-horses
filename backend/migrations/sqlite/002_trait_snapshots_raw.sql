-- ADR-0012: сохранение исходного предсказания движка при подтверждении человеком.
-- raw_predicted_traits — предсказание как есть, traits — подтверждённые/отредактированные значения.
ALTER TABLE trait_snapshots ADD COLUMN raw_predicted_traits TEXT NOT NULL DEFAULT 'null';

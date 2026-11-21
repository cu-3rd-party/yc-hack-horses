ALTER TABLE observations ADD COLUMN name TEXT NOT NULL DEFAULT '';

-- Local review state includes the ranked candidates and decisions for this image.
CREATE TABLE comparisons (
    id TEXT PRIMARY KEY,
    state TEXT NOT NULL
);

-- Only confirmed reference photos enter the catalog and the observation event.
CREATE TABLE observation_photos (
    id TEXT PRIMARY KEY,
    observation_id TEXT NOT NULL REFERENCES observations(id),
    sha256 TEXT NOT NULL,
    name TEXT NOT NULL,
    annotation TEXT NOT NULL,
    model_version TEXT NOT NULL,
    embedding TEXT NOT NULL
);
CREATE INDEX idx_photos_observation ON observation_photos(observation_id);
CREATE INDEX idx_photos_model ON observation_photos(model_version);

-- Replay confirmed reference data that an older peer journaled before upgrade.
UPDATE observations o SET name = e.payload ->> 'name' FROM events e
WHERE e.event_type = 'observation.added' AND e.aggregate_id = o.id
  AND jsonb_typeof(e.payload -> 'name') = 'string';

INSERT INTO observation_photos (id, observation_id, sha256, name, annotation, model_version, embedding)
SELECT p ->> 'photo_id', o.id, p ->> 'blob_sha256', COALESCE(p ->> 'name', ''),
       (p -> 'annotation')::text, p ->> 'model_version', (p -> 'embedding')::text
FROM events e JOIN observations o ON o.id = e.aggregate_id
CROSS JOIN LATERAL jsonb_array_elements(e.payload -> 'photos') p
WHERE e.event_type = 'observation.added'
ON CONFLICT(id) DO NOTHING;

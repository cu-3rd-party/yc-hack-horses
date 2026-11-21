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

-- An older peer may already have journaled a reviewed observation from an
-- upgraded peer. Rebuild its missing projection from that original event.
UPDATE observations AS o
SET name = json_extract(CAST(e.payload AS TEXT), '$.name')
FROM events AS e
WHERE e.event_type = 'observation.added' AND e.aggregate_id = o.id
  AND json_type(CAST(e.payload AS TEXT), '$.name') = 'text';

INSERT INTO observation_photos (id, observation_id, sha256, name, annotation, model_version, embedding)
SELECT p.value ->> 'photo_id', o.id, p.value ->> 'blob_sha256', COALESCE(p.value ->> 'name', ''),
       p.value -> 'annotation', p.value ->> 'model_version', p.value -> 'embedding'
FROM events e
JOIN observations o ON o.id = e.aggregate_id
JOIN json_each(CAST(e.payload AS TEXT), '$.photos') p
WHERE e.event_type = 'observation.added'
ON CONFLICT(id) DO NOTHING;

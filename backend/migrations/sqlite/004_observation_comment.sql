ALTER TABLE observations ADD COLUMN comment TEXT NOT NULL DEFAULT '';

-- Recover comments received from newer peers before this node upgraded.
UPDATE observations AS o
SET comment = json_extract(CAST(e.payload AS TEXT), '$.comment')
FROM events AS e
WHERE e.event_type = 'observation.added' AND e.aggregate_id = o.id
  AND json_type(CAST(e.payload AS TEXT), '$.comment') = 'text';

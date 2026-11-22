ALTER TABLE observations ADD COLUMN comment TEXT NOT NULL DEFAULT '';

-- Recover comments received from newer peers before this node upgraded.
UPDATE observations o SET comment = e.payload ->> 'comment' FROM events e
WHERE e.event_type = 'observation.added' AND e.aggregate_id = o.id
  AND jsonb_typeof(e.payload -> 'comment') = 'string';

ALTER TABLE events
    ADD COLUMN webhook_body text;

UPDATE events
SET webhook_body = jsonb_build_object(
    'id', id,
    'type', event_type,
    'created_at', created_at,
    'data', payload
)::text;

ALTER TABLE events
    ALTER COLUMN webhook_body SET NOT NULL;

ALTER TABLE deliveries
    ADD COLUMN lease_until timestamptz;

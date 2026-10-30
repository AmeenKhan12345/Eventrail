ALTER TABLE endpoints
    ADD COLUMN event_types text[] NOT NULL DEFAULT '{}';

CREATE TABLE deliveries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    event_id uuid NOT NULL REFERENCES events (id),
    endpoint_id uuid NOT NULL REFERENCES endpoints (id),
    status text NOT NULL DEFAULT 'pending',
    attempt_count int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CONSTRAINT deliveries_status_check CHECK (status IN ('pending', 'succeeded', 'failed')),
    CONSTRAINT deliveries_event_id_endpoint_id_key UNIQUE (event_id, endpoint_id)
);

CREATE INDEX deliveries_next_attempt_at_idx
    ON deliveries (next_attempt_at)
    WHERE status = 'pending';

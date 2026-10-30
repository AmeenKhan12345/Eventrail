CREATE TABLE events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    idempotency_key text,
    request_fingerprint text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX events_tenant_id_idempotency_key_idx
    ON events (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX events_tenant_id_created_at_idx
    ON events (tenant_id, created_at DESC);

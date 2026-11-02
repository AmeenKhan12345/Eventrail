CREATE TABLE delivery_attempts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_id uuid NOT NULL REFERENCES deliveries (id),
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    attempt_number int NOT NULL,
    status_code int,
    latency_ms int NOT NULL,
    failure_kind text NOT NULL,
    error_text text NOT NULL DEFAULT '',
    response_excerpt text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT delivery_attempts_delivery_id_attempt_number_key UNIQUE (delivery_id, attempt_number)
);

CREATE INDEX delivery_attempts_delivery_id_idx
    ON delivery_attempts (delivery_id);

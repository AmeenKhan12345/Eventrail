CREATE TABLE endpoints (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    url text NOT NULL,
    description text NOT NULL DEFAULT '',
    is_active boolean NOT NULL DEFAULT true,
    secret_ciphertext bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE UNIQUE INDEX endpoints_tenant_id_url_idx
    ON endpoints (tenant_id, url)
    WHERE deleted_at IS NULL;

CREATE INDEX endpoints_tenant_id_idx
    ON endpoints (tenant_id)
    WHERE deleted_at IS NULL;

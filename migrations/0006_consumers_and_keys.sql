CREATE TABLE consumers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name        TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 120),
    description TEXT NOT NULL DEFAULT '',
    owner_email TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    revision    BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, id),
    UNIQUE (org_id, name)
);

CREATE TABLE api_keys (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    consumer_id UUID NOT NULL,
    key_prefix  TEXT NOT NULL UNIQUE CHECK (key_prefix ~ '^gwf_[a-zA-Z0-9]{8,20}$'),
    key_hash    TEXT NOT NULL UNIQUE CHECK (length(key_hash) >= 64),
    label       TEXT NOT NULL DEFAULT '',
    scopes      TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    expires_at  TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (org_id, consumer_id) REFERENCES consumers(org_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_consumers_org_status ON consumers (org_id, status);
CREATE INDEX idx_api_keys_org_consumer ON api_keys (org_id, consumer_id);
CREATE INDEX idx_api_keys_active_prefix ON api_keys (key_prefix) WHERE revoked_at IS NULL;

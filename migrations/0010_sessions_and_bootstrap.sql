CREATE TABLE system_bootstrap (
    singleton       BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton = TRUE),
    initial_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    initial_org_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    completed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE CHECK (length(token_hash) = 64),
    csrf_token  TEXT NOT NULL CHECK (length(csrf_token) >= 32),
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sessions_user_active ON sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_sessions_expires ON sessions (expires_at);

CREATE TABLE idempotency_keys (
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) BETWEEN 8 AND 128),
    request_method  TEXT NOT NULL,
    request_path    TEXT NOT NULL,
    request_hash    TEXT NOT NULL,
    response_status INTEGER NOT NULL,
    response_body   JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, idempotency_key)
);

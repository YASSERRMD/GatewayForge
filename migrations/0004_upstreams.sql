CREATE TABLE upstream_pools (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id                   UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    env_id                   UUID NOT NULL,
    name                     TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 120),
    destination_class        TEXT NOT NULL DEFAULT 'internal_approved'
                             CHECK (destination_class IN ('public', 'internal_approved', 'mock_local')),
    connect_timeout_ms       INTEGER NOT NULL DEFAULT 3000 CHECK (connect_timeout_ms BETWEEN 100 AND 60000),
    read_timeout_ms          INTEGER NOT NULL DEFAULT 15000 CHECK (read_timeout_ms BETWEEN 100 AND 120000),
    health_check_path        TEXT NOT NULL DEFAULT '/health' CHECK (health_check_path ~ '^/'),
    health_check_interval_ms INTEGER NOT NULL DEFAULT 10000 CHECK (health_check_interval_ms BETWEEN 1000 AND 300000),
    health_check_timeout_ms  INTEGER NOT NULL DEFAULT 2000 CHECK (health_check_timeout_ms BETWEEN 100 AND 30000),
    revision                 BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, id),
    UNIQUE (org_id, env_id, name),
    FOREIGN KEY (org_id, env_id) REFERENCES environments(org_id, id) ON DELETE RESTRICT
);

CREATE TABLE upstream_targets (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    pool_id    UUID NOT NULL,
    url        TEXT NOT NULL CHECK (url ~ '^https?://[^[:space:]]+$'),
    weight     INTEGER NOT NULL DEFAULT 100 CHECK (weight BETWEEN 1 AND 1000),
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (pool_id, url),
    FOREIGN KEY (org_id, pool_id) REFERENCES upstream_pools(org_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_upstream_pools_org_env ON upstream_pools (org_id, env_id);
CREATE INDEX idx_upstream_targets_pool ON upstream_targets (org_id, pool_id);

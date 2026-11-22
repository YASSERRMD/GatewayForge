CREATE TABLE routes (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    env_id           UUID NOT NULL,
    service_id       UUID NOT NULL,
    upstream_pool_id UUID NOT NULL,
    name             TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 120),
    host             TEXT NOT NULL CHECK (host = lower(trim(host)) AND length(host) BETWEEN 1 AND 253),
    path_prefix      TEXT NOT NULL CHECK (path_prefix ~ '^/'),
    methods          TEXT[] NOT NULL CHECK (
                         cardinality(methods) >= 1
                         AND methods <@ ARRAY['GET','HEAD','POST','PUT','PATCH','DELETE','OPTIONS']::TEXT[]
                     ),
    strip_prefix     BOOLEAN NOT NULL DEFAULT FALSE,
    priority         INTEGER NOT NULL DEFAULT 100 CHECK (priority BETWEEN 0 AND 10000),
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    revision         BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, id),
    UNIQUE (org_id, env_id, name),
    UNIQUE (org_id, env_id, host, path_prefix, methods, priority),
    FOREIGN KEY (org_id, env_id) REFERENCES environments(org_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (org_id, service_id) REFERENCES services(org_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, upstream_pool_id) REFERENCES upstream_pools(org_id, id) ON DELETE RESTRICT
);

CREATE INDEX idx_routes_org_env_priority
    ON routes (org_id, env_id, priority DESC, length(path_prefix) DESC, id ASC);
CREATE INDEX idx_routes_service ON routes (org_id, service_id);
CREATE INDEX idx_routes_pool ON routes (org_id, upstream_pool_id);

CREATE TABLE environments (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    slug          TEXT NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,31}$'),
    display_name  TEXT NOT NULL CHECK (length(trim(display_name)) BETWEEN 1 AND 80),
    is_production BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, id),
    UNIQUE (org_id, slug)
);

CREATE TABLE services (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    env_id      UUID NOT NULL,
    slug        TEXT NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    name        TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 120),
    description TEXT NOT NULL DEFAULT '',
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    revision    BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1),
    deleted_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, id),
    FOREIGN KEY (org_id, env_id) REFERENCES environments(org_id, id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX uq_services_active_slug
    ON services (org_id, env_id, slug)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_services_org_env_active
    ON services (org_id, env_id, created_at DESC)
    WHERE deleted_at IS NULL;

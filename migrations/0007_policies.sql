CREATE TABLE policies (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name           TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 120),
    policy_type    TEXT NOT NULL CHECK (
                       policy_type IN (
                           'api_key',
                           'jwt',
                           'rate_limit',
                           'quota',
                           'retry',
                           'timeout',
                           'circuit_breaker',
                           'cors',
                           'header_transform'
                       )
                   ),
    schema_version INTEGER NOT NULL DEFAULT 1 CHECK (schema_version = 1),
    config         JSONB NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    enabled        BOOLEAN NOT NULL DEFAULT TRUE,
    revision       BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, id),
    UNIQUE (org_id, name)
);

CREATE TABLE policy_attachments (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    policy_id  UUID NOT NULL,
    scope_type TEXT NOT NULL CHECK (scope_type IN ('org', 'service', 'route')),
    service_id UUID,
    route_id   UUID,
    priority   INTEGER NOT NULL DEFAULT 100 CHECK (priority BETWEEN 0 AND 10000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (scope_type = 'org'     AND service_id IS NULL     AND route_id IS NULL) OR
        (scope_type = 'service' AND service_id IS NOT NULL AND route_id IS NULL) OR
        (scope_type = 'route'   AND service_id IS NULL     AND route_id IS NOT NULL)
    ),
    FOREIGN KEY (org_id, policy_id) REFERENCES policies(org_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, service_id) REFERENCES services(org_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, route_id) REFERENCES routes(org_id, id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX uq_policy_attach_org
    ON policy_attachments (org_id, policy_id)
    WHERE scope_type = 'org';

CREATE UNIQUE INDEX uq_policy_attach_service
    ON policy_attachments (org_id, policy_id, service_id)
    WHERE scope_type = 'service';

CREATE UNIQUE INDEX uq_policy_attach_route
    ON policy_attachments (org_id, policy_id, route_id)
    WHERE scope_type = 'route';

CREATE INDEX idx_policies_org_type ON policies (org_id, policy_type);
CREATE INDEX idx_policy_attachments_route ON policy_attachments (org_id, route_id) WHERE route_id IS NOT NULL;
CREATE INDEX idx_policy_attachments_service ON policy_attachments (org_id, service_id) WHERE service_id IS NOT NULL;

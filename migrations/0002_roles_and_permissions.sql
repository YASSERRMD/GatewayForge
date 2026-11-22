CREATE TABLE roles (
    name        TEXT PRIMARY KEY,
    description TEXT NOT NULL,
    rank        SMALLINT NOT NULL UNIQUE CHECK (rank BETWEEN 1 AND 100)
);

INSERT INTO roles (name, description, rank) VALUES
    ('owner',     'Full organization administration, membership management, publish, and rollback', 100),
    ('admin',     'Manage resources, consumers, keys, policies, publish, and rollback', 75),
    ('developer', 'Manage draft services, upstreams, routes, policies, and test console', 50),
    ('viewer',    'Read-only access to configuration, telemetry, and audit trail', 10);

ALTER TABLE memberships
    ADD CONSTRAINT fk_memberships_role
    FOREIGN KEY (role) REFERENCES roles(name);

CREATE TABLE role_permissions (
    role       TEXT NOT NULL REFERENCES roles(name) ON DELETE CASCADE,
    permission TEXT NOT NULL,
    PRIMARY KEY (role, permission)
);

INSERT INTO role_permissions (role, permission) VALUES
    -- viewer
    ('viewer', 'org:read'),
    ('viewer', 'member:read'),
    ('viewer', 'config:read'),
    ('viewer', 'consumer:read'),
    ('viewer', 'release:read'),
    ('viewer', 'audit:read'),
    ('viewer', 'metrics:read'),
    -- developer
    ('developer', 'org:read'),
    ('developer', 'member:read'),
    ('developer', 'config:read'),
    ('developer', 'config:write'),
    ('developer', 'consumer:read'),
    ('developer', 'release:read'),
    ('developer', 'audit:read'),
    ('developer', 'metrics:read'),
    ('developer', 'console:use'),
    -- admin
    ('admin', 'org:read'),
    ('admin', 'org:update'),
    ('admin', 'member:read'),
    ('admin', 'member:write'),
    ('admin', 'config:read'),
    ('admin', 'config:write'),
    ('admin', 'consumer:read'),
    ('admin', 'consumer:write'),
    ('admin', 'key:manage'),
    ('admin', 'release:read'),
    ('admin', 'release:publish'),
    ('admin', 'release:activate'),
    ('admin', 'release:rollback'),
    ('admin', 'audit:read'),
    ('admin', 'metrics:read'),
    ('admin', 'console:use'),
    -- owner
    ('owner', 'org:read'),
    ('owner', 'org:update'),
    ('owner', 'org:delete'),
    ('owner', 'member:read'),
    ('owner', 'member:write'),
    ('owner', 'member:owner_grant'),
    ('owner', 'config:read'),
    ('owner', 'config:write'),
    ('owner', 'consumer:read'),
    ('owner', 'consumer:write'),
    ('owner', 'key:manage'),
    ('owner', 'release:read'),
    ('owner', 'release:publish'),
    ('owner', 'release:activate'),
    ('owner', 'release:rollback'),
    ('owner', 'audit:read'),
    ('owner', 'metrics:read'),
    ('owner', 'console:use');

CREATE TABLE audit_events (
    id              BIGSERIAL PRIMARY KEY,
    org_id          UUID REFERENCES organizations(id) ON DELETE RESTRICT,
    actor_user_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_email     TEXT NOT NULL DEFAULT '',
    action          TEXT NOT NULL CHECK (length(trim(action)) BETWEEN 1 AND 120),
    target_type     TEXT NOT NULL CHECK (length(trim(target_type)) BETWEEN 1 AND 80),
    target_id       TEXT NOT NULL DEFAULT '',
    request_id      TEXT NOT NULL CHECK (length(trim(request_id)) BETWEEN 1 AND 128),
    before_redacted JSONB,
    after_redacted  JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION enforce_audit_events_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only (% is forbidden)', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_audit_events_append_only
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW
    EXECUTE FUNCTION enforce_audit_events_append_only();

CREATE INDEX idx_audit_events_org_created_id
    ON audit_events (org_id, created_at DESC, id DESC);

CREATE INDEX idx_audit_events_org_action
    ON audit_events (org_id, action, created_at DESC);

CREATE INDEX idx_audit_events_org_target
    ON audit_events (org_id, target_type, target_id);

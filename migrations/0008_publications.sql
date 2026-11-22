CREATE TABLE draft_revisions (
    org_id     UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    revision   BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1),
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE publications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    version         INTEGER NOT NULL CHECK (version >= 1),
    draft_revision  BIGINT NOT NULL CHECK (draft_revision >= 1),
    snapshot_json   JSONB NOT NULL CHECK (jsonb_typeof(snapshot_json) = 'object'),
    checksum_sha256 TEXT NOT NULL CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
    notes           TEXT NOT NULL DEFAULT '',
    published_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    published_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, id),
    UNIQUE (org_id, version)
);

CREATE OR REPLACE FUNCTION enforce_publications_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'publications rows are immutable (% is forbidden)', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_publications_immutable
    BEFORE UPDATE OR DELETE ON publications
    FOR EACH ROW
    EXECUTE FUNCTION enforce_publications_immutable();

CREATE TABLE active_publications (
    org_id              UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    publication_id      UUID NOT NULL,
    activation_revision BIGINT NOT NULL DEFAULT 1 CHECK (activation_revision >= 1),
    activated_by        UUID REFERENCES users(id) ON DELETE SET NULL,
    activated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (org_id, publication_id) REFERENCES publications(org_id, id) ON DELETE RESTRICT
);

CREATE TABLE publication_activations (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    publication_id UUID NOT NULL,
    is_rollback    BOOLEAN NOT NULL DEFAULT FALSE,
    reason         TEXT NOT NULL DEFAULT '',
    activated_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    activated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (org_id, publication_id) REFERENCES publications(org_id, id) ON DELETE RESTRICT
);

CREATE INDEX idx_publications_org_version ON publications (org_id, version DESC);
CREATE INDEX idx_publication_activations_org ON publication_activations (org_id, activated_at DESC);

-- +goose Up
CREATE TABLE organizations (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    slug text NOT NULL,
    plan text NOT NULL DEFAULT 'free',
    analytics_store text NOT NULL DEFAULT 'duckdb',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT organizations_name_not_empty CHECK (btrim(name) <> ''),
    CONSTRAINT organizations_name_length CHECK (char_length(name) <= 120),
    CONSTRAINT organizations_slug_format CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$'),
    CONSTRAINT organizations_plan CHECK (plan IN ('free', 'pro', 'ultra')),
    CONSTRAINT organizations_analytics_store CHECK (analytics_store IN ('duckdb', 'clickhouse')),
    CONSTRAINT organizations_paid_store CHECK (
        analytics_store = 'duckdb' OR plan IN ('pro', 'ultra')
    )
);

CREATE UNIQUE INDEX organizations_slug_unique ON organizations (slug);

CREATE TABLE organization_memberships (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id),
    CONSTRAINT organization_memberships_role CHECK (role IN ('owner', 'admin', 'member'))
);

CREATE INDEX organization_memberships_user_idx ON organization_memberships (user_id);

CREATE TABLE databases (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name text NOT NULL,
    slug text NOT NULL,
    connection_ciphertext bytea NOT NULL,
    connection_nonce bytea NOT NULL,
    encryption_key_version smallint NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT databases_name_not_empty CHECK (btrim(name) <> ''),
    CONSTRAINT databases_name_length CHECK (char_length(name) <= 120),
    CONSTRAINT databases_slug_format CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$'),
    CONSTRAINT databases_connection_ciphertext_not_empty CHECK (octet_length(connection_ciphertext) > 0),
    CONSTRAINT databases_connection_nonce_length CHECK (octet_length(connection_nonce) = 12),
    CONSTRAINT databases_connection_ciphertext_length CHECK (
        octet_length(connection_ciphertext) BETWEEN 17 AND 4112
    ),
    CONSTRAINT databases_encryption_key_version_positive CHECK (encryption_key_version > 0)
);

CREATE UNIQUE INDEX databases_org_slug_unique ON databases (organization_id, slug);
CREATE INDEX databases_org_idx ON databases (organization_id);

CREATE TABLE database_extensions (
    database_id uuid NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
    extension text NOT NULL,
    selected_by uuid NOT NULL REFERENCES users(id),
    selected_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (database_id, extension),
    CONSTRAINT database_extensions_supported CHECK (
        extension IN (
            'pg_stat_statements',
            'pg_stat_monitor',
            'pgstattuple',
            'pg_buffercache',
            'pg_nygma'
        )
    )
);

-- +goose StatementBegin
CREATE FUNCTION enforce_free_organization_membership() RETURNS trigger AS $$
DECLARE
    organization_plan text;
    member_count bigint;
BEGIN
    SELECT plan INTO organization_plan
    FROM organizations
    WHERE id = NEW.organization_id
    FOR UPDATE;

    IF organization_plan = 'free' THEN
        SELECT count(*) INTO member_count
        FROM organization_memberships
        WHERE organization_id = NEW.organization_id
          AND user_id <> NEW.user_id;

        IF member_count >= 1 THEN
            RAISE EXCEPTION 'free organizations support one member'
                USING ERRCODE = '23514', CONSTRAINT = 'free_organization_single_member';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER organization_memberships_free_limit
BEFORE INSERT OR UPDATE ON organization_memberships
FOR EACH ROW EXECUTE FUNCTION enforce_free_organization_membership();

-- +goose StatementBegin
CREATE FUNCTION enforce_organization_downgrade() RETURNS trigger AS $$
BEGIN
    IF NEW.plan = 'free' AND OLD.plan <> 'free' AND (
        SELECT count(*) FROM organization_memberships
        WHERE organization_id = NEW.id
    ) > 1 THEN
        RAISE EXCEPTION 'remove additional members before downgrading to free'
            USING ERRCODE = '23514', CONSTRAINT = 'free_organization_single_member';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER organizations_downgrade_guard
BEFORE UPDATE OF plan ON organizations
FOR EACH ROW EXECUTE FUNCTION enforce_organization_downgrade();

-- +goose Down
DROP TRIGGER IF EXISTS organizations_downgrade_guard ON organizations;
DROP FUNCTION IF EXISTS enforce_organization_downgrade();
DROP TRIGGER IF EXISTS organization_memberships_free_limit ON organization_memberships;
DROP FUNCTION IF EXISTS enforce_free_organization_membership();
DROP TABLE IF EXISTS database_extensions;
DROP TABLE IF EXISTS databases;
DROP TABLE IF EXISTS organization_memberships;
DROP TABLE IF EXISTS organizations;

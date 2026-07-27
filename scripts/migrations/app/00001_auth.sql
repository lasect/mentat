-- +goose Up
CREATE TABLE users (
    id uuid PRIMARY KEY,
    email text NOT NULL,
    email_verified_at timestamptz,
    display_name text NOT NULL DEFAULT '',
    avatar_url text,
    disabled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_email_normalized CHECK (email = lower(btrim(email))),
    CONSTRAINT users_email_not_empty CHECK (email <> ''),
    CONSTRAINT users_email_length CHECK (char_length(email) <= 254),
    CONSTRAINT users_display_name_length CHECK (char_length(display_name) <= 120),
    CONSTRAINT users_avatar_url_length CHECK (avatar_url IS NULL OR char_length(avatar_url) <= 2048)
);

CREATE UNIQUE INDEX users_email_unique ON users (email);

CREATE TABLE password_credentials (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT password_credentials_hash_not_empty CHECK (password_hash <> '')
);

CREATE TABLE oauth_identities (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider text NOT NULL,
    provider_user_id text NOT NULL,
    provider_email text NOT NULL,
    email_verified boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT oauth_identities_provider CHECK (provider IN ('github', 'google')),
    CONSTRAINT oauth_identities_provider_user_id_not_empty CHECK (provider_user_id <> ''),
    CONSTRAINT oauth_identities_provider_user_id_length CHECK (char_length(provider_user_id) <= 255),
    CONSTRAINT oauth_identities_provider_email_normalized CHECK (provider_email = lower(btrim(provider_email))),
    CONSTRAINT oauth_identities_provider_email_length CHECK (
        provider_email <> '' AND char_length(provider_email) <= 254
    )
);

CREATE UNIQUE INDEX oauth_identities_provider_subject_unique
    ON oauth_identities (provider, provider_user_id);
CREATE UNIQUE INDEX oauth_identities_user_provider_unique
    ON oauth_identities (user_id, provider);

CREATE TABLE refresh_sessions (
    id uuid PRIMARY KEY,
    family_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    replaced_by uuid REFERENCES refresh_sessions(id) ON DELETE SET NULL,
    user_agent text NOT NULL DEFAULT '',
    ip_address text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    CONSTRAINT refresh_sessions_token_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT refresh_sessions_expiry_order CHECK (expires_at > created_at),
    CONSTRAINT refresh_sessions_user_agent_length CHECK (char_length(user_agent) <= 512),
    CONSTRAINT refresh_sessions_ip_address_length CHECK (char_length(ip_address) <= 64)
);

CREATE UNIQUE INDEX refresh_sessions_token_hash_unique ON refresh_sessions (token_hash);
CREATE INDEX refresh_sessions_user_active_idx
    ON refresh_sessions (user_id, expires_at)
    WHERE revoked_at IS NULL;
CREATE INDEX refresh_sessions_family_idx ON refresh_sessions (family_id);

-- +goose Down
DROP TABLE IF EXISTS refresh_sessions;
DROP TABLE IF EXISTS oauth_identities;
DROP TABLE IF EXISTS password_credentials;
DROP TABLE IF EXISTS users;

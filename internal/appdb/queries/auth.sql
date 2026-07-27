-- name: CreateUser :one
INSERT INTO users (id, email, email_verified_at, display_name, avatar_url)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: LockUserByID :one
SELECT * FROM users WHERE id = $1 FOR UPDATE;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: MarkUserEmailVerified :exec
UPDATE users
SET email_verified_at = COALESCE(email_verified_at, $2), updated_at = now()
WHERE id = $1;

-- name: UpdateUserProfileFromOAuth :exec
UPDATE users
SET display_name = CASE
        WHEN sqlc.arg(display_name)::text <> '' THEN sqlc.arg(display_name)
        ELSE display_name
    END,
    avatar_url = COALESCE(sqlc.narg(avatar_url), avatar_url),
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: CreatePasswordCredential :exec
INSERT INTO password_credentials (user_id, password_hash)
VALUES ($1, $2);

-- name: GetPasswordCredentialByEmail :one
SELECT u.*, pc.password_hash
FROM users u
JOIN password_credentials pc ON pc.user_id = u.id
WHERE u.email = $1;

-- name: HasPasswordCredentialForUser :one
SELECT EXISTS (
    SELECT 1 FROM password_credentials WHERE user_id = $1
);

-- name: GetOAuthIdentity :one
SELECT * FROM oauth_identities
WHERE provider = $1 AND provider_user_id = $2;

-- name: GetOAuthIdentityForUser :one
SELECT * FROM oauth_identities
WHERE user_id = $1 AND provider = $2;

-- name: CreateOAuthIdentity :one
INSERT INTO oauth_identities (
    id, user_id, provider, provider_user_id, provider_email, email_verified
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListOAuthProvidersForUser :many
SELECT provider FROM oauth_identities
WHERE user_id = $1
ORDER BY provider;

-- name: CreateRefreshSession :one
INSERT INTO refresh_sessions (
    id, family_id, user_id, token_hash, expires_at, user_agent, ip_address
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetRefreshSessionByTokenHash :one
SELECT * FROM refresh_sessions WHERE token_hash = $1;

-- name: RotateRefreshSession :execrows
UPDATE refresh_sessions
SET revoked_at = $2, replaced_by = $3, last_used_at = $2
WHERE id = $1 AND revoked_at IS NULL AND expires_at > $2;

-- name: RevokeRefreshFamily :exec
UPDATE refresh_sessions
SET revoked_at = COALESCE(revoked_at, $2)
WHERE family_id = $1;

-- name: RevokeAllUserSessions :exec
UPDATE refresh_sessions
SET revoked_at = COALESCE(revoked_at, $2)
WHERE user_id = $1;

-- name: GetSessionWithUser :one
SELECT
    s.id AS session_id,
    s.revoked_at AS session_revoked_at,
    s.expires_at AS session_expires_at,
    u.*
FROM refresh_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.id = $1;

-- name: LockSessionWithUser :one
SELECT
    s.id AS session_id,
    s.revoked_at AS session_revoked_at,
    s.expires_at AS session_expires_at,
    u.*
FROM refresh_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.id = $1
FOR SHARE OF s;

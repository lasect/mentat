-- name: CreateOrganization :one
INSERT INTO organizations (id, name, slug)
VALUES ($1, $2, $3)
RETURNING *;

-- name: CreateOrganizationMembership :one
INSERT INTO organization_memberships (organization_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetOrganizationBySlugForUser :one
SELECT o.*, m.role
FROM organizations o
JOIN organization_memberships m ON m.organization_id = o.id
WHERE o.slug = $1 AND m.user_id = $2;

-- name: LockOrganizationBySlugForUser :one
SELECT o.*, m.role
FROM organizations o
JOIN organization_memberships m ON m.organization_id = o.id
WHERE o.slug = $1 AND m.user_id = $2
FOR UPDATE OF o
FOR SHARE OF m;

-- name: GetOrganizationByIDForUser :one
SELECT o.*, m.role
FROM organizations o
JOIN organization_memberships m ON m.organization_id = o.id
WHERE o.id = $1 AND m.user_id = $2;

-- name: ListOrganizationsForUser :many
SELECT o.*, m.role
FROM organizations o
JOIN organization_memberships m ON m.organization_id = o.id
WHERE m.user_id = $1
ORDER BY o.created_at, o.id;

-- name: UpdateOrganizationAnalyticsStore :one
UPDATE organizations
SET analytics_store = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateDatabase :one
INSERT INTO databases (
    id, organization_id, name, slug, connection_ciphertext,
    connection_nonce, encryption_key_version, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetDatabaseForOrganization :one
SELECT * FROM databases
WHERE id = $1 AND organization_id = $2;

-- name: GetDatabaseBySlugForOrganization :one
SELECT * FROM databases
WHERE slug = $1 AND organization_id = $2
FOR UPDATE;

-- name: ListDatabasesForOrganization :many
SELECT * FROM databases
WHERE organization_id = $1
ORDER BY created_at, id;

-- name: DeleteDatabase :execrows
DELETE FROM databases
WHERE id = $1 AND organization_id = $2;

-- name: SelectDatabaseExtension :exec
INSERT INTO database_extensions (database_id, extension, selected_by)
VALUES ($1, $2, $3)
ON CONFLICT (database_id, extension) DO UPDATE
SET selected_by = EXCLUDED.selected_by, selected_at = now();

-- name: DeselectDatabaseExtension :execrows
DELETE FROM database_extensions
WHERE database_id = $1 AND extension = $2;

-- name: ListDatabaseExtensions :many
SELECT extension FROM database_extensions
WHERE database_id = $1
ORDER BY extension;

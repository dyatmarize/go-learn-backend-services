-- name: GetUserByID :one
SELECT *
FROM core_user
WHERE id = $1
  AND deleted_at IS NULL;

-- name: GetUserByUUID :one
SELECT *
FROM core_user
WHERE uuid = $1
  AND deleted_at IS NULL;

-- name: GetUserByEmail :one
SELECT *
FROM core_user
WHERE email = $1
  AND deleted_at IS NULL;

-- name: GetUserByRole :many
SELECT *
FROM core_user
WHERE role_id = $1
  AND deleted_at IS NULL;

-- name: ListUsers :many
SELECT *
FROM core_user
WHERE deleted_at IS NULL
ORDER BY id LIMIT $1
OFFSET $2;

-- name: CountUsers :one
SELECT count(*)
FROM core_user
WHERE deleted_at IS NULL;

-- name: CreateUser :one
INSERT INTO core_user (role_id, name, email, password, status, uuid)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: UpdateUser :one
UPDATE core_user
SET name       = $2,
    email      = $3,
    role_id    = $4,
    status     = $5,
    updated_at = now()
WHERE uuid = $1
  AND deleted_at IS NULL RETURNING *;

-- name: SoftDeleteUser :execrows
UPDATE core_user
SET deleted_at = now(),
    updated_at = now()
WHERE uuid = $1
  AND deleted_at IS NULL;
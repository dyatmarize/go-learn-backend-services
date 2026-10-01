-- name: ListRoles :many
SELECT *
FROM core_user_role
WHERE deleted_at IS NULL
ORDER BY role_level;

-- name: GetRoleByID :one
SELECT *
FROM core_user_role
WHERE id = $1
  AND deleted_at IS NULL;
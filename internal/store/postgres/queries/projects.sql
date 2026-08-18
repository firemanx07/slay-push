-- name: GetProjectBySlug :one
select * from projects where slug = $1;

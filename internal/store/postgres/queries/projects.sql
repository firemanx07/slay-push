-- name: GetProjectBySlug :one
select * from projects where slug = $1;

-- name: CreateProject :one
insert into projects (name, slug)
values ($1, $2)
returning *;

-- name: GetProjectBySlug :one
select * from projects where slug = $1;

-- name: CreateProject :one
insert into projects (name, slug)
values ($1, $2)
returning *;

-- name: GetProjectByID :one
select * from projects where id = $1;

-- name: ListProjects :many
select * from projects order by created_at desc;

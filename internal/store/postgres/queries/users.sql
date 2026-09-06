-- name: CreateUser :one
insert into users (email, password_hash)
values ($1, $2)
returning *;

-- name: GetUserByEmail :one
select * from users where email = $1;

-- name: GetUserByID :one
select * from users where id = $1;

-- name: CountUsers :one
select count(*) from users;

-- name: UpdateUserPassword :execrows
update users set password_hash = $2 where email = $1;

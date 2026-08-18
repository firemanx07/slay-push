-- name: CreateSession :one
insert into sessions (user_id, token_hash, expires_at)
values ($1, $2, $3)
returning *;

-- name: GetActiveSessionByHash :one
select * from sessions
where token_hash = $1
  and revoked_at is null
  and expires_at > now();

-- name: TouchSessionLastUsed :exec
update sessions set last_used_at = now() where id = $1;

-- name: RevokeSession :exec
update sessions set revoked_at = now() where id = $1;

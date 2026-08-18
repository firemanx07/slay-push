-- name: CreateAPIKey :one
insert into api_keys (project_id, name, key_prefix, key_hash, scope)
values ($1, $2, $3, $4, $5)
returning *;

-- name: GetActiveAPIKeyByHash :one
select ak.* from api_keys ak
join projects p on p.id = ak.project_id
where ak.key_hash = $1
  and ak.revoked_at is null
  and (ak.expires_at is null or ak.expires_at > now())
  and p.status = 'active';

-- name: TouchAPIKeyLastUsed :exec
update api_keys set last_used_at = now() where id = $1;

-- name: RevokeAPIKey :exec
update api_keys set revoked_at = now() where id = $1 and project_id = $2;

-- name: ListAPIKeysByProject :many
select * from api_keys where project_id = $1 order by created_at desc;

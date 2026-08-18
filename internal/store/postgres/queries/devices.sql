-- name: UpsertDevice :one
insert into devices (project_id, token, platform, provider_type, metadata)
values ($1, $2, $3, $4, $5)
on conflict (project_id, token) do update set
    platform = excluded.platform,
    provider_type = excluded.provider_type,
    metadata = excluded.metadata,
    status = 'active',
    updated_at = now()
returning *;

-- name: GetDevicesByIDs :many
select * from devices where project_id = $1 and id = any(sqlc.arg(ids)::uuid[]);

-- name: MarkDeviceStatus :exec
update devices set status = $2, updated_at = now() where id = $1;

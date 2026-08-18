-- name: UpsertDevice :one
-- subscriber_id keeps its existing value when omitted.
insert into devices (project_id, token, platform, provider_type, metadata, subscriber_id)
values ($1, $2, $3, $4, $5, sqlc.narg(subscriber_id))
on conflict (project_id, token) do update set
    platform = excluded.platform,
    provider_type = excluded.provider_type,
    metadata = excluded.metadata,
    subscriber_id = coalesce(excluded.subscriber_id, devices.subscriber_id),
    status = 'active',
    updated_at = now()
returning *;

-- name: GetDevicesByIDs :many
select * from devices where project_id = $1 and id = any(sqlc.arg(ids)::uuid[]);

-- name: MarkDeviceStatus :exec
update devices set status = $2, updated_at = now() where id = $1;

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

-- name: AdvisoryLockDeviceUUID :exec
-- Transaction-scoped: released automatically at commit/rollback.
select pg_advisory_xact_lock(hashtextextended(sqlc.arg(lock_key)::text, 0));

-- name: MarkStaleDevicesByDeviceUUID :many
-- Marks stale every other active device under the same subscriber that
-- shares the given device_uuid, excluding the device row just registered.
update devices
set status = 'stale', updated_at = now()
where project_id = $1
  and subscriber_id = $2
  and id != $3
  and status = 'active'
  and metadata ->> 'device_uuid' = sqlc.arg(device_uuid)::text
returning *;

-- name: ListDevicesByProject :many
-- external_id/status filters are skipped when passed as an empty string.
select d.id, d.project_id, d.token, d.platform, d.provider_type, d.status,
    d.metadata, d.created_at, d.updated_at, d.subscriber_id,
    coalesce(s.external_id, '') as external_id
from devices d
left join subscribers s on s.id = d.subscriber_id
where d.project_id = $1
  and (sqlc.arg(external_id)::text = '' or s.external_id = sqlc.arg(external_id)::text)
  and (sqlc.arg(status)::text = '' or d.status = sqlc.arg(status)::text)
order by d.created_at desc
limit sqlc.arg(page_limit)::int offset sqlc.arg(page_offset)::int;

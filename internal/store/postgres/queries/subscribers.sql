-- name: UpsertSubscriber :one
insert into subscribers (project_id, external_id)
values ($1, $2)
on conflict (project_id, external_id) do update set
    last_seen_at = now()
returning *;

-- name: GetActiveDevicesBySubscriberExternalIDs :many
-- Active devices under subscribed subscribers matching any of the given
-- external ids, scoped to the project.
select d.* from devices d
join subscribers s on s.id = d.subscriber_id
where s.project_id = $1
  and s.external_id = any(sqlc.arg(external_ids)::text[])
  and s.status = 'subscribed'
  and d.status = 'active';

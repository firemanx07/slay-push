-- name: CreateNotification :one
-- Returns no rows on an idempotency-key conflict; the caller falls back to
-- GetNotificationByIdempotencyKey, same as InsertNotificationRecipient's
-- on-conflict-do-nothing + fallback-fetch pattern.
insert into notifications (project_id, idempotency_key, title, body, data, target_spec)
values ($1, $2, $3, $4, $5, $6)
on conflict (project_id, idempotency_key) where idempotency_key is not null do nothing
returning *;

-- name: GetNotificationByIdempotencyKey :one
select * from notifications where project_id = $1 and idempotency_key = $2;

-- name: GetNotification :one
select * from notifications where id = $1 and project_id = $2;

-- name: GetNotificationByID :one
-- Unscoped by project: only for the dashboard, which is visible to every
-- project by design (see internal/dashboard's visibleProjects).
select * from notifications where id = $1;

-- name: SetNotificationStatus :exec
update notifications set status = $2 where id = $1;

-- name: SetNotificationTotals :exec
update notifications set total_recipients = $2 where id = $1;

-- name: CompleteNotification :exec
update notifications set status = $2, completed_at = now() where id = $1;

-- name: CompleteNotificationIfAllRecipientsTerminal :execrows
update notifications n
set status = 'completed', completed_at = now()
where n.id = $1
  and n.status = 'processing'
  and n.total_recipients > 0
  and (select count(*) from notification_recipients r where r.notification_id = n.id) = n.total_recipients
  and not exists (
    select 1 from notification_recipients r
    where r.notification_id = n.id and r.status not in ('sent', 'delivered', 'failed')
  );

-- name: ListNotificationsByProject :many
select * from notifications
where project_id = $1
order by created_at desc
limit sqlc.arg(page_limit)::int offset sqlc.arg(page_offset)::int;

-- name: CreateNotification :one
insert into notifications (project_id, idempotency_key, title, body, data, target_spec)
values ($1, $2, $3, $4, $5, $6)
returning *;

-- name: GetNotificationByIdempotencyKey :one
select * from notifications where project_id = $1 and idempotency_key = $2;

-- name: GetNotification :one
select * from notifications where id = $1 and project_id = $2;

-- name: SetNotificationStatus :exec
update notifications set status = $2 where id = $1;

-- name: SetNotificationTotals :exec
update notifications set total_recipients = $2 where id = $1;

-- name: CompleteNotification :exec
update notifications set status = $2, completed_at = now() where id = $1;

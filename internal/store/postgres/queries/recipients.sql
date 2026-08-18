-- name: InsertNotificationRecipient :one
insert into notification_recipients (notification_id, device_id, provider_type)
values ($1, $2, $3)
on conflict (notification_id, device_id) do nothing
returning *;

-- name: GetRecipientByNotificationAndDevice :one
select * from notification_recipients where notification_id = $1 and device_id = $2;

-- name: GetNotificationRecipient :one
select * from notification_recipients where id = $1;

-- name: ListNotificationRecipients :many
select * from notification_recipients where notification_id = $1 order by created_at;

-- name: MarkRecipientSending :one
update notification_recipients
set status = 'sending', attempt_count = attempt_count + 1
where id = $1
returning *;

-- name: MarkRecipientSent :exec
update notification_recipients
set status = 'sent', provider_message_id = $2, sent_at = now()
where id = $1;

-- name: MarkRecipientFailed :exec
update notification_recipients
set status = 'failed', error_code = $2, error_message = $3, failed_at = now()
where id = $1;

-- name: CountRecipientStatuses :many
select status, count(*) as count from notification_recipients where notification_id = $1 group by status;

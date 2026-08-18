-- name: UpsertProviderCredential :one
insert into provider_credentials (project_id, provider_type, environment, credential)
values ($1, $2, $3, $4)
on conflict (project_id, provider_type, environment) do update set
    credential = excluded.credential,
    is_active = true,
    updated_at = now()
returning *;

-- name: GetActiveProviderCredential :one
select * from provider_credentials
where project_id = $1 and provider_type = $2 and environment = $3 and is_active
limit 1;

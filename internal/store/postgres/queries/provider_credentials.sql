-- name: UpsertProviderCredential :one
insert into provider_credentials (project_id, provider_type, environment, credential, wrapped_dek)
values ($1, $2, $3, $4, $5)
on conflict (project_id, provider_type, environment) do update set
    credential = excluded.credential,
    wrapped_dek = excluded.wrapped_dek,
    is_active = true,
    updated_at = now()
returning *;

-- name: GetActiveProviderCredential :one
select * from provider_credentials
where project_id = $1 and provider_type = $2 and environment = $3 and is_active
limit 1;

-- name: ListProviderCredentialsByProject :many
-- Never selects credential/wrapped_dek: this feeds the dashboard, which
-- must never see ciphertext or key material.
select id, project_id, provider_type, environment, is_active, created_at, updated_at
from provider_credentials
where project_id = $1
order by provider_type;

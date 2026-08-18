create table api_keys (
    id            uuid primary key default gen_random_uuid(),
    project_id    uuid not null references projects(id),
    name          text not null,
    key_prefix    text not null,
    key_hash      text not null unique,
    scope         text not null check (scope in ('read', 'send')),
    created_at    timestamptz not null default now(),
    last_used_at  timestamptz,
    revoked_at    timestamptz,
    expires_at    timestamptz
);

create index api_keys_project_id_idx on api_keys (project_id);

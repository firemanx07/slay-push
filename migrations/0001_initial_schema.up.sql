-- Initial schema, consolidated (pre-release — no incremental migration
-- history to preserve). Every row currently hangs off a single seeded
-- "default" project.

create extension if not exists pgcrypto;

create table projects (
    id         uuid primary key default gen_random_uuid(),
    name       text not null,
    slug       text not null unique,
    status     text not null default 'active',
    created_at timestamptz not null default now()
);

-- A subscriber is the grouping/identity concept (one external_id); a
-- device is one channel-specific delivery endpoint belonging to a
-- subscriber.
create table subscribers (
    id            uuid primary key default gen_random_uuid(),
    project_id    uuid not null references projects(id),
    external_id   text not null,
    status        text not null default 'subscribed' check (status in ('subscribed', 'opted_out')),
    created_at    timestamptz not null default now(),
    last_seen_at  timestamptz not null default now(),
    unique (project_id, external_id),
    unique (project_id, id)
);

create table devices (
    id            uuid primary key default gen_random_uuid(),
    project_id    uuid not null references projects(id),
    subscriber_id uuid,
    token         text not null,
    -- Device kind, distinct from provider_type (which push network to use).
    platform      text not null check (platform in ('ios', 'android', 'web')),
    provider_type text not null check (provider_type in ('expo', 'fcm', 'apns', 'hms')),
    status        text not null default 'active' check (status in ('active', 'stale', 'invalid')),
    -- Optional device details: brand, os_version, device_uuid, language,
    -- coords, last_seen_ip. See internal/transport/http/devices.go.
    metadata      jsonb not null default '{}'::jsonb,
    created_at    timestamptz not null default now(),
    updated_at    timestamptz not null default now(),
    unique (project_id, token),
    foreign key (project_id, subscriber_id) references subscribers (project_id, id)
);

create table notifications (
    id              uuid primary key default gen_random_uuid(),
    project_id      uuid not null references projects(id),
    idempotency_key text,
    title           text,
    body            text,
    data            jsonb not null default '{}'::jsonb,
    target_spec     jsonb not null,
    status          text not null default 'pending'
        check (status in ('pending', 'fanning_out', 'processing', 'completed', 'failed')),
    total_recipients int not null default 0,
    created_at      timestamptz not null default now(),
    completed_at    timestamptz
);

-- Prevents a client-retried POST with the same idempotency_key from
-- creating a duplicate notification. Null is allowed to repeat.
create unique index notifications_project_idempotency_key_idx
    on notifications (project_id, idempotency_key)
    where idempotency_key is not null;

create table notification_recipients (
    id                  uuid primary key default gen_random_uuid(),
    notification_id     uuid not null references notifications(id),
    device_id           uuid not null references devices(id),
    provider_type       text not null,
    status              text not null default 'queued'
        check (status in ('queued', 'sending', 'sent', 'delivered', 'failed')),
    provider_message_id text,
    error_code          text,
    error_message       text,
    attempt_count       int not null default 0,
    sent_at             timestamptz,
    failed_at           timestamptz,
    created_at          timestamptz not null default now(),
    -- Idempotency anchor: a retried fanout can't insert two recipient
    -- rows for the same device.
    unique (notification_id, device_id)
);

-- credential is envelope-encrypted: each row gets its own AES-256-GCM Data
-- Encryption Key (DEK), which wraps the actual credential; wrapped_dek
-- stores that DEK, itself wrapped by APP_MASTER_KEY. credential is bytea
-- (AES-GCM ciphertext is arbitrary binary, not valid JSON/UTF8 text).
create table provider_credentials (
    id            uuid primary key default gen_random_uuid(),
    project_id    uuid not null references projects(id),
    provider_type text not null check (provider_type in ('expo', 'fcm', 'apns', 'hms')),
    environment   text not null default 'production',
    credential    bytea not null,
    wrapped_dek   bytea not null,
    is_active     boolean not null default true,
    created_at    timestamptz not null default now(),
    updated_at    timestamptz not null default now(),
    unique (project_id, provider_type, environment)
);

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

create table users (
    id            uuid primary key default gen_random_uuid(),
    email         text not null unique,
    password_hash text not null,
    -- Reserved for future RBAC; unenforced in v1 (every user is an admin).
    role          text not null default 'admin',
    created_at    timestamptz not null default now()
);

create table sessions (
    id            uuid primary key default gen_random_uuid(),
    user_id       uuid not null references users(id),
    token_hash    text not null unique,
    created_at    timestamptz not null default now(),
    last_used_at  timestamptz,
    expires_at    timestamptz not null,
    revoked_at    timestamptz
);

create index sessions_user_id_idx on sessions (user_id);

-- Seed default project for devices/credentials to attach to.
insert into projects (id, name, slug)
values ('00000000-0000-0000-0000-000000000001', 'Default Project', 'default')
on conflict (slug) do nothing;

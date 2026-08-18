-- Initial schema: projects, devices, notifications, notification_recipients,
-- provider_credentials. Every row currently hangs off a single seeded
-- "default" project.
--
-- provider_credentials.credential is plain JSONB. Do not store real
-- production credentials in this table until encryption at rest is added.

create extension if not exists pgcrypto;

create table projects (
    id         uuid primary key default gen_random_uuid(),
    name       text not null,
    slug       text not null unique,
    status     text not null default 'active',
    created_at timestamptz not null default now()
);

create table devices (
    id            uuid primary key default gen_random_uuid(),
    project_id    uuid not null references projects(id),
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
    unique (project_id, token)
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

create table provider_credentials (
    id            uuid primary key default gen_random_uuid(),
    project_id    uuid not null references projects(id),
    provider_type text not null check (provider_type in ('expo', 'fcm', 'apns', 'hms')),
    environment   text not null default 'production',
    credential    jsonb not null,
    is_active     boolean not null default true,
    created_at    timestamptz not null default now(),
    updated_at    timestamptz not null default now(),
    unique (project_id, provider_type, environment)
);

-- Seed default project for devices/credentials to attach to.
insert into projects (id, name, slug)
values ('00000000-0000-0000-0000-000000000001', 'Default Project', 'default')
on conflict (slug) do nothing;

-- Subscriber/device split (Phase 2): a subscriber is the grouping/identity
-- concept (one external_id set by the integrator), a device is one
-- channel-specific delivery endpoint belonging to a subscriber. Enables
-- group push: send to a subscriber, fan out to every active device
-- beneath it.

create table subscribers (
    id            uuid primary key default gen_random_uuid(),
    project_id    uuid not null references projects(id),
    external_id   text not null,
    status        text not null default 'subscribed' check (status in ('subscribed', 'opted_out')),
    created_at    timestamptz not null default now(),
    last_seen_at  timestamptz not null default now(),
    unique (project_id, external_id)
);

alter table devices add column subscriber_id uuid references subscribers(id);

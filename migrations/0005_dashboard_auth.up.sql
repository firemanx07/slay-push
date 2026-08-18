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

-- Authentication, accounts and plans.
--
-- Email addresses are stored lower-case (checked below) and compared with
-- lower(email), which gives the same behaviour as citext without requiring a
-- Postgres extension. Provider subject identifiers are never exposed by the
-- account API.

create table users (
    id                uuid primary key default gen_random_uuid(),
    email             text,
    email_verified    boolean not null default false,
    display_name      text,
    avatar_url        text,
    role              text not null default 'user'
                      check (role in ('user', 'admin')),
    status            text not null default 'active'
                      check (status in ('active', 'suspended', 'deleted')),
    terms_version     text,
    terms_accepted_at timestamptz,
    created_at        timestamptz not null default now(),
    updated_at        timestamptz not null default now(),
    last_login_at     timestamptz,
    deleted_at        timestamptz,
    check (email is null or email = lower(email))
);

create unique index users_email_active_uidx
    on users (lower(email))
    where email is not null and deleted_at is null;

create table user_identities (
    id             uuid primary key default gen_random_uuid(),
    user_id        uuid not null references users (id) on delete cascade,
    provider       text not null
                   check (provider in ('google', 'apple', 'facebook')),
    subject        text not null,
    email          text,
    email_verified boolean not null default false,
    display_name   text,
    avatar_url     text,
    created_at     timestamptz not null default now(),
    last_login_at  timestamptz,
    unique (provider, subject),
    check (email is null or email = lower(email))
);

create index user_identities_user_idx on user_identities (user_id);

create table oauth_flows (
    id            uuid primary key default gen_random_uuid(),
    provider      text not null
                  check (provider in ('google', 'apple', 'facebook')),
    intent        text not null default 'login'
                  check (intent in ('login', 'link')),
    user_id       uuid references users (id) on delete cascade,
    state_hash    bytea not null unique,
    browser_hash  bytea not null,
    nonce         text not null,
    code_verifier text not null,
    redirect_uri  text not null,
    return_to     text,
    created_at    timestamptz not null default now(),
    expires_at    timestamptz not null,
    consumed_at   timestamptz
);

create index oauth_flows_expiry_idx on oauth_flows (expires_at);

create table sessions (
    id                uuid primary key default gen_random_uuid(),
    user_id           uuid not null references users (id) on delete cascade,
    kind              text not null check (kind in ('web', 'mobile')),
    user_agent        text,
    ip                text,
    created_at        timestamptz not null default now(),
    last_used_at      timestamptz not null default now(),
    idle_expires_at   timestamptz not null,
    absolute_expires_at timestamptz not null,
    revoked_at        timestamptz,
    revoked_reason    text,
    csrf_token_hash   bytea not null
);

create index sessions_user_idx on sessions (user_id);

create table session_tokens (
    id           uuid primary key default gen_random_uuid(),
    session_id   uuid not null references sessions (id) on delete cascade,
    kind         text not null check (kind in ('web', 'access', 'refresh')),
    token_hash   bytea not null unique,
    issued_at    timestamptz not null default now(),
    expires_at   timestamptz not null,
    rotated_at   timestamptz,
    replaced_by  uuid references session_tokens (id) on delete set null,
    revoked_at   timestamptz
);

create index session_tokens_session_idx on session_tokens (session_id);
create index session_tokens_expiry_idx on session_tokens (expires_at);

create table plans (
    code       text primary key,
    name       text not null,
    limits     jsonb not null,
    active     boolean not null default true,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create table subscriptions (
    user_id            uuid primary key references users (id) on delete cascade,
    plan_code          text not null references plans (code),
    status             text not null
                       check (status in ('active', 'trialing', 'past_due', 'canceled')),
    source             text not null default 'manual'
                       check (source in ('manual', 'stripe', 'apple', 'google')),
    current_period_end timestamptz,
    overrides          jsonb not null default '{}'::jsonb,
    created_at         timestamptz not null default now(),
    updated_at         timestamptz not null default now()
);

-- Placeholder limits from docs/users-auth-tiers.md. Plan assignment remains a
-- manual operation until billing lands.
insert into plans (code, name, limits) values
    ('free', 'Free', '{
        "max_active_watches": 3,
        "allowed_modes": ["basic"],
        "min_interval_seconds": 900,
        "ai_setup_previews_per_day": 0,
        "ai_checks_per_day": 0,
        "history_days": 30
    }'::jsonb),
    ('plus', 'Plus', '{
        "max_active_watches": 25,
        "allowed_modes": ["basic", "scripted"],
        "min_interval_seconds": 300,
        "ai_setup_previews_per_day": 0,
        "ai_checks_per_day": 0,
        "history_days": 180
    }'::jsonb),
    ('pro', 'Pro', '{
        "max_active_watches": 100,
        "allowed_modes": ["basic", "scripted", "ai_assisted"],
        "min_interval_seconds": 60,
        "ai_setup_previews_per_day": 5,
        "ai_checks_per_day": 100,
        "history_days": 365
    }'::jsonb)
on conflict (code) do nothing;

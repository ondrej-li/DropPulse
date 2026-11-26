-- Append-only per-check execution logs and administrator audit trail.

create table event_checks (
    id          bigint generated always as identity primary key,
    event_id    uuid not null references events (id) on delete cascade,
    checked_at  timestamptz not null default now(),
    trigger     text not null default 'scheduled'
                check (trigger in ('scheduled', 'manual', 'admin_manual')),
    outcome     text not null
                check (outcome in ('ok', 'error', 'blocked')),
    is_on       boolean not null,
    status_code integer,
    detail      text not null default '',
    error       text not null default '',
    duration_ms integer not null default 0 check (duration_ms >= 0)
);

create index event_checks_event_time_idx
    on event_checks (event_id, checked_at desc);

create index event_checks_time_idx
    on event_checks (checked_at desc);

create table admin_audit_log (
    id         bigint generated always as identity primary key,
    actor_id   uuid references users (id) on delete set null,
    action     text not null,
    target_id  uuid,
    request_id text not null default '',
    metadata   jsonb not null default '{}'::jsonb,
    created_at timestamptz not null default now()
);

create index admin_audit_log_time_idx
    on admin_audit_log (created_at desc);

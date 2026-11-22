-- Local single-user events and their HTTPS availability checks.
--
-- A watch is an event whose availability is the status code of one HTTPS
-- request: exactly 200 means on. `notified_on` is the notification edge state,
-- not history: it is cleared whenever the event goes off, so the next off->on
-- transition sends another email. `last_notified_at` keeps the last successful
-- notification for display.

alter table users add column username text;

create unique index users_username_active_uidx
    on users (username)
    where username is not null and deleted_at is null;

create table events (
    id                     uuid primary key default gen_random_uuid(),
    user_id                uuid not null references users (id) on delete cascade,
    name                   text not null,
    url                    text not null,
    check_interval_seconds integer not null default 300
                           check (check_interval_seconds between 60 and 86400),
    enabled                boolean not null default true,
    is_on                  boolean not null default false,
    notified_on            boolean not null default false,
    last_checked_at        timestamptz,
    last_status_code       integer,
    last_error             text not null default '',
    last_notified_at       timestamptz,
    last_notification_error text not null default '',
    created_at             timestamptz not null default now(),
    updated_at             timestamptz not null default now(),
    check (name <> ''),
    check (url like 'https://%')
);

create index events_user_idx on events (user_id, created_at desc);
create index events_due_idx on events (last_checked_at) where enabled;

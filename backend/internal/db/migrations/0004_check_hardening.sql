-- Check outcome classification, exponential backoff, challenge pausing, and
-- per-watcher check statistics.

alter table events
    add column consecutive_failures integer not null default 0
                                    check (consecutive_failures >= 0),
    add column consecutive_blocks   integer not null default 0
                                    check (consecutive_blocks >= 0),
    add column next_check_at        timestamptz,
    add column last_outcome         text not null default ''
                                    check (last_outcome in ('', 'ok', 'error', 'blocked')),
    add column last_detail          text not null default '',
    add column paused_reason        text not null default '',
    add column check_count          bigint not null default 0
                                    check (check_count >= 0),
    add column error_count          bigint not null default 0
                                    check (error_count >= 0),
    add column last_duration_ms     integer
                                    check (last_duration_ms is null or last_duration_ms >= 0);

create index events_next_check_idx
    on events (next_check_at)
    where enabled;

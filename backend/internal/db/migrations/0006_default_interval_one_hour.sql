-- Default newly created watches to a one-hour (3600s) check interval.

alter table events
    alter column check_interval_seconds set default 3600;

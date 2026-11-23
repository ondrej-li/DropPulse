-- Extend the account role set with the non-administrative account types.
--
-- Every user already has a NOT NULL role defaulting to 'user'; this migration
-- only widens the allowed values to user, admin, vendor, agent and ai. Existing
-- rows keep their current role, and new OAuth sign-ups still default to 'user'.
-- Only 'admin' carries administrative access; the other types are labels for
-- the kind of actor the account represents and are assigned administratively.

alter table users drop constraint users_role_check;

alter table users add constraint users_role_check
    check (role in ('user', 'admin', 'vendor', 'agent', 'ai'));

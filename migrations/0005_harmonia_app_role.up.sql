-- Makes the append-only guarantee on events real. 0001_init.up.sql's
-- comment already described this ("Application role gets no
-- UPDATE/DELETE on events — enforced at the database level"), but the
-- REVOKE was left commented out and the role itself was never created —
-- the application has been connecting as the migration-owning superuser
-- this whole time, which can UPDATE/DELETE anything, events included.
--
-- This is a real environment change, not just a schema one: the
-- application's own runtime connection (HARMONIA_DATABASE_URL) must be
-- switched to this new role after this migration runs — see
-- .env.example, docker-compose.yml, and the README's "Local setup"
-- section. Migrations keep running as the existing superuser role
-- (harmonia_app has no CREATE/ALTER/role-management privileges, by
-- design — the normal admin-role-runs-migrations,
-- restricted-role-runs-the-app split).
--
-- Dev-only credential: password matches the role name, same convention
-- as every other dev-only secret already in this repo (e.g. the
-- Postgres superuser is itself "harmonia"/"harmonia"). Rotate this for
-- any environment that isn't a local dev sandbox.
CREATE ROLE harmonia_app LOGIN PASSWORD 'harmonia_app';

GRANT USAGE ON SCHEMA public TO harmonia_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO harmonia_app;
-- events.id is the only serial/bigserial column in this schema — every
-- other primary key is a uuid via gen_random_uuid(), which needs no
-- sequence grant. Without this, the app couldn't INSERT into events at
-- all under the restricted role (nextval() needs USAGE on the sequence).
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO harmonia_app;

-- The actual enforcement: append-only, for real, not just by convention.
REVOKE UPDATE, DELETE ON events FROM harmonia_app;

-- The one narrow, explicit exception: deleting a room deletes its own
-- history along with it, via the existing ON DELETE CASCADE chain
-- (agents/tasks/events/handoffs all cascade from rooms.id — see
-- 0001_init.up.sql). SECURITY DEFINER runs this DELETE as this
-- function's owner (the superuser that runs this migration), not as
-- whatever role calls it — that's what lets harmonia_app reach this one
-- cascade path without ever holding DELETE on events directly.
--
-- SET search_path pinned explicitly, and the table reference schema-
-- qualified on top of that: an unset search_path in a SECURITY DEFINER
-- function is a classic Postgres privilege-escalation bug (a caller
-- with CREATE on some schema earlier in their own search_path could
-- otherwise shadow "rooms" with an object of their own choosing). Belt
-- and suspenders here rather than relying on either alone.
CREATE FUNCTION delete_room_cascade(p_room_id uuid) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $func$
BEGIN
    DELETE FROM public.rooms WHERE id = p_room_id;
END;
$func$;

-- SECURITY DEFINER functions are EXECUTE-able by PUBLIC by default —
-- left alone, that default would hand every future non-superuser role a
-- way to bypass events' own DELETE restriction through this function,
-- not just harmonia_app. Revoke that default first, then grant to
-- exactly the one role meant to have it.
REVOKE EXECUTE ON FUNCTION delete_room_cascade(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION delete_room_cascade(uuid) TO harmonia_app;

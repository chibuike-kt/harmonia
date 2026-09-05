REVOKE EXECUTE ON FUNCTION delete_room_cascade(uuid) FROM harmonia_app;
DROP FUNCTION delete_room_cascade(uuid);

REVOKE ALL ON ALL TABLES IN SCHEMA public FROM harmonia_app;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM harmonia_app;
REVOKE USAGE ON SCHEMA public FROM harmonia_app;

DROP ROLE IF EXISTS harmonia_app;

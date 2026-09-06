-- Real token usage capture for agent-authored messages — both Anthropic
-- and OpenAI already return input/output token counts on every
-- response; this just starts reading and persisting them. Nullable, and
-- only ever populated for sender_kind = 'agent' rows: a human message
-- has no generation behind it to meter. Stored directly on messages
-- rather than a separate usage table — this ties usage to the exact
-- generation that produced it with no join required, and there's
-- nothing else a per-message usage row would ever need to reference.
--
-- No grant migration needed here: harmonia_app's grant on messages is
-- table-level (0007_messages_grants.up.sql), so it already covers these
-- new columns — unlike 0008_decisions, which needed its own grant
-- because it's a brand new table.
ALTER TABLE messages
    ADD COLUMN input_tokens integer,
    ADD COLUMN output_tokens integer;

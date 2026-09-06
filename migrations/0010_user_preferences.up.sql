-- Two new nullable fields on users, per ADR-005 (settings modal):
--
-- preferred_name: what the app and agents call someone casually,
-- distinct from username (the OAuth-derived account handle) and
-- display_name (their real/full name). Feeds the dashboard's rotating
-- greeting, previously hardcoded to a literal name.
--
-- custom_instructions: free text, prepended into every agent Generate
-- call's context (reply generation and title generation alike) the same
-- way the recency-window message history already gets assembled — see
-- internal/message's own context-building code.
--
-- Neither is touched by the GitHub/Google OAuth upsert path — they're
-- pure user-set preferences with no provider-side equivalent to sync
-- from, unlike username/display_name/avatar_url/email.
ALTER TABLE users
    ADD COLUMN preferred_name text,
    ADD COLUMN custom_instructions text;

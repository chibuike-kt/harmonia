-- Web search toggle and per-message file attachments. See
-- docs/adr/ADR-008-search-and-file-tools.md.

ALTER TABLE rooms ADD COLUMN web_search_enabled boolean NOT NULL DEFAULT false;

ALTER TABLE messages ADD COLUMN attachment_content bytea;
ALTER TABLE messages ADD COLUMN attachment_filename text;
ALTER TABLE messages ADD COLUMN attachment_mime_type text;

-- Defense in depth alongside application-layer validation — a message
-- row should never carry more than ~1MB of attachment content. Adjust
-- the literal if the real product needs differ once this is live.
ALTER TABLE messages ADD CONSTRAINT messages_attachment_size_check
    CHECK (attachment_content IS NULL OR octet_length(attachment_content) <= 1048576);

-- No new grant needed — messages already has harmonia_app's full grant
-- from migration 0007; ALTER TABLE ADD COLUMN doesn't require a fresh
-- one. rooms already has its own grant from 0005 for the same reason.

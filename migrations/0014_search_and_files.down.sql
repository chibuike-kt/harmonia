ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_attachment_size_check;
ALTER TABLE messages DROP COLUMN IF EXISTS attachment_content;
ALTER TABLE messages DROP COLUMN IF EXISTS attachment_filename;
ALTER TABLE messages DROP COLUMN IF EXISTS attachment_mime_type;
ALTER TABLE rooms DROP COLUMN IF EXISTS web_search_enabled;

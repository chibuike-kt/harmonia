-- Phase 2: human accounts, sessions, and BYOK provider credentials.
-- See docs/adr/ADR-002-user-auth-and-byok-credentials.md for the reasoning.

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    github_id     text UNIQUE,
    google_id     text UNIQUE,
    username      text NOT NULL,
    display_name  text,
    avatar_url    text,
    email         text,
    created_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT users_has_provider CHECK (github_id IS NOT NULL OR google_id IS NOT NULL)
);

-- Opaque session tokens — same pattern as agents.api_key_hash: random
-- bytes, SHA-256 hash stored, hash looked up on each request. See
-- ADR-002 for why this isn't a JWT.
CREATE TABLE sessions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash    text NOT NULL UNIQUE,
    user_agent    text,
    ip_address    text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz
);

-- One credential per user per provider. encrypted_key/nonce are
-- AES-256-GCM output — see ADR-002. key_hint is the only part of the
-- key ever returned by the API.
CREATE TABLE provider_credentials (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider       text NOT NULL,
    encrypted_key  bytea NOT NULL,
    nonce          bytea NOT NULL,
    key_hint       text NOT NULL,
    verified_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT provider_credentials_unique_per_provider UNIQUE (user_id, provider)
);

-- Nullable on purpose — Milestone 1 rooms predate human accounts.
ALTER TABLE rooms ADD COLUMN owner_id uuid REFERENCES users(id);

CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_provider_credentials_user_id ON provider_credentials(user_id);
CREATE INDEX idx_rooms_owner_id ON rooms(owner_id);

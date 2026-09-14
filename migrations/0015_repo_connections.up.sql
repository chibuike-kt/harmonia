-- Repo connection for the IDE direction. See
-- docs/adr/ADR-009-ide-foundations.md.

CREATE TABLE repo_connections (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id                uuid NOT NULL UNIQUE REFERENCES rooms(id) ON DELETE CASCADE,
    repo_owner             text NOT NULL,
    repo_name              text NOT NULL,
    default_branch         text NOT NULL,
    github_installation_id text NOT NULL,
    connected_by_user_id   uuid NOT NULL REFERENCES users(id),
    created_at             timestamptz NOT NULL DEFAULT now()
);

-- One repo per room for now — matches the ADR's "one room, two views of
-- one shared workspace" framing. A room without a row here just has no
-- Code view content yet, not an error state.

CREATE INDEX idx_repo_connections_room_id ON repo_connections(room_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON repo_connections TO harmonia_app;

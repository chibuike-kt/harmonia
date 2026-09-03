-- Milestone 1 schema. Postgres is the source of truth; see docs/adr/ADR-001.

CREATE TABLE rooms (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    status      text NOT NULL DEFAULT 'active',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE agents (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id       uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    name          text NOT NULL,
    provider      text NOT NULL,               -- 'anthropic' | 'openai'
    capabilities  jsonb NOT NULL DEFAULT '[]',
    status        text NOT NULL DEFAULT 'available',
    api_key_hash  text NOT NULL,                -- scoped credential, hashed at rest
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tasks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id         uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    owner_agent_id  uuid REFERENCES agents(id),
    parent_task_id  uuid REFERENCES tasks(id),
    objective       text NOT NULL,
    status          text NOT NULL DEFAULT 'CREATED',
    created_at      timestamptz NOT NULL DEFAULT now(),
    claimed_at      timestamptz,
    completed_at    timestamptz,

    CONSTRAINT tasks_status_check CHECK (
        status IN ('CREATED', 'QUEUED', 'CLAIMED', 'RUNNING', 'COMPLETED', 'FAILED', 'BLOCKED')
    )
);

-- Append-only audit trail. No UPDATE/DELETE grants on this table for the
-- application role — enforced below, not just by convention.
CREATE TABLE events (
    id          bigserial PRIMARY KEY,
    room_id     uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    task_id     uuid REFERENCES tasks(id),
    agent_id    uuid REFERENCES agents(id),
    type        text NOT NULL,
    payload     jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE handoffs (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id        uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    task_id        uuid NOT NULL REFERENCES tasks(id),
    from_agent_id  uuid NOT NULL REFERENCES agents(id),
    to_agent_id    uuid NOT NULL REFERENCES agents(id),
    summary        text NOT NULL,
    completed      jsonb NOT NULL DEFAULT '[]',
    remaining      jsonb NOT NULL DEFAULT '[]',
    artifacts      jsonb NOT NULL DEFAULT '[]',
    decisions      jsonb NOT NULL DEFAULT '[]',
    risks          jsonb NOT NULL DEFAULT '[]',
    status         text NOT NULL DEFAULT 'REQUESTED',
    created_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT handoffs_status_check CHECK (
        status IN ('REQUESTED', 'ACCEPTED', 'REJECTED')
    )
);

CREATE INDEX idx_tasks_room_id ON tasks(room_id);
CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_events_room_id ON events(room_id);
CREATE INDEX idx_events_task_id ON events(task_id);
CREATE INDEX idx_events_created_at ON events(created_at);
CREATE INDEX idx_handoffs_room_id ON handoffs(room_id);

-- Application role gets no UPDATE/DELETE on events — append-only, enforced
-- at the database level. Create/apply this role as part of environment setup;
-- migrations run as a superuser/owner role that bypasses it, by design.
-- REVOKE UPDATE, DELETE ON events FROM harmonia_app;

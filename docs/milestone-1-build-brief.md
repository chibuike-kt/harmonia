# Milestone 1 Build Brief — for Claude Code

**Read first, in this order:** `CLAUDE.md`, `docs/adr/ADR-001-language-decision.md`,
then the Milestone 1 design doc (the source of truth for scope — ask Kingsley
for it if it isn't already in `docs/` in this repo). This brief tells you
what to build and in what order; the design doc tells you why the schema and
protocol look the way they do. Don't re-derive either — follow them.

## Objective

Make the acceptance test in the design doc's section 11 actually pass, end
to end, against the real Postgres/Redis containers already running. That
test is the definition of done for this milestone — nothing else is.

## What already exists — do not redesign it

- Full data model, migrated (`migrations/0001_init.up.sql`)
- Domain packages with working `Store` types: `internal/{room,agent,task,event,handoff,contextengine}`
- `internal/task.Store.Claim` — the atomic claim query. This is the whole
  concurrency story for this milestone. Do not add leases, locks, or retry
  logic around it.
- `internal/event.Store` — append-only by omission. Do not add Update/Delete
  methods to it, ever, for any reason.
- `internal/protocol` — AACP v0 envelope and the seven-operation set
- `internal/provider` — the `Agent` interface; `anthropic`/`openai` clients
  exist but `Generate` is `panic("not implemented")`
- Docker Compose: `postgres`, `redis`, `api` (bare-binary image, no Go
  toolchain inside it), `migrate` (one-shot, `profiles: ["tools"]`)
- `tests/concurrency/task_claim_test.go` — already exercises `Claim` under
  20 concurrent goroutines. Should already pass against the running Postgres.
  Run it first, before writing anything new, to confirm the baseline holds.

## What's explicitly out of scope — do not build any of this now

Autonomous agent decision-making about *when* to delegate, rooms UI, memory
promotion, tool execution/sandboxing, event bus, semantic memory, SDK,
protocol binary encoding, lease expiry, multi-org tenancy, agent reputation.
If a task seems to need one of these, stop and flag it rather than building
a lightweight version of it.

## Scope decision: scripted orchestration, real model calls

Full autonomous delegation (agents deciding on their own when to create a
subtask or hand off) is Phase 2 work per the original product overview, not
this milestone. For Milestone 1, the *sequence* of operations in the
acceptance test is driven by a test harness, not by agent judgment — but the
task objectives, research content, and handoff summaries that flow through
that sequence come from real calls to the Anthropic and OpenAI APIs, not
fixtures. This proves the protocol and shared state work with real
AI-generated content moving through them, without requiring an orchestration
intelligence that doesn't exist yet. Don't build more autonomy than this —
that's scope creep against this milestone, not thoroughness.

## Build order

Each of these is one PR, one concern, matching `CONTRIBUTING.md`'s commit
conventions. Don't batch them.

1. **`feat(agent): scoped API key issuance and auth middleware`**
   `agent.Store.Register` returns an agent row but no plaintext key today.
   Generate a random key at registration, return it once in the response
   body, store only its hash (the column already exists). Add chi middleware
   that reads `Authorization: Bearer <key>`, hashes it, looks up the agent,
   and rejects (401) on no match or a room mismatch with the requested
   resource.

2. **`feat(room): HTTP handler for room creation`**
   `POST /v1/rooms` → `room.Store.Create`.

3. **`feat(agent): HTTP handler for registration`**
   `POST /v1/rooms/{room_id}/agents` → `agent.Store.Register`, wrapped with
   the key issuance from step 1.

4. **`feat(task): HTTP handlers + event recording`**
   `POST /v1/tasks`, `POST /v1/tasks/{id}/claim`, `POST /v1/tasks/{id}/complete`.
   Every one of these also calls `event.Store.Record` with the matching
   `TASK_CREATED` / `TASK_CLAIMED` / `TASK_COMPLETED` type and a payload
   shaped like the `protocol.Envelope`'s `Payload` field — construct an
   actual `protocol.Envelope` value for the operation and store its payload,
   don't invent an ad hoc shape here.

5. **`feat(contextengine): HTTP handler for context queries`**
   `GET /v1/context/tasks/{task_id}` → `contextengine.Store.TaskByID`,
   recording `CONTEXT_REQUESTED` / the response isn't itself a stored event
   in the current schema — record the request only, matching what
   `events` actually models today. If you find you need to record the
   response too, that's a schema change — flag it, don't add a column
   silently.

6. **`feat(handoff): HTTP handlers + event recording`**
   `POST /v1/handoffs` (request), `POST /v1/handoffs/{id}/accept`, each
   recording `HANDOFF_REQUESTED` / `HANDOFF_ACCEPTED`.

7. **`feat(event): HTTP handler for room event history`**
   `GET /v1/rooms/{id}/events` → `event.Store.ListByRoom`. This is what the
   acceptance test's step 8 asserts against.

8. **`feat(provider/anthropic): implement Generate`**
   Real call to the Messages API, non-streaming. `ANTHROPIC_API_KEY` from
   env, already wired into the `api` container. No tool use, no retries
   beyond what the SDK/HTTP client gives you for free — that's Phase 4.

9. **`feat(provider/openai): implement Generate`**
   Same shape, against Chat Completions or Responses API — pick one and
   note which in the commit body.

10. **`test(integration): Milestone 1 acceptance test`**
    A new test (or small `cmd/harness` driver, your call) that walks section
    11's eight steps against the live HTTP API, using the real provider
    clients for step content, and asserts the final event log is complete
    and ordered. This is the test that proves the milestone — it should be
    the last thing you write, and everything above should make it pass on
    the first real run, not require patching.

## Constraints that apply to every step above

- Follow `CLAUDE.md` and `CONTRIBUTING.md` as written — atomic commits, no
  AI attribution, stage and stop, the maintainer commits.
- No agent ever receives another agent's API key or the raw provider
  credentials. The server holds `ANTHROPIC_API_KEY`/`OPENAI_API_KEY`; agents
  authenticate to the platform with their own scoped key only.
- Every new endpoint gets a test before it's considered done — not
  necessarily integration-level for every one, but at minimum enough to
  catch a regression in that endpoint's behavior.
- If a step's honest implementation needs something not listed here (a new
  table column, a new dependency, a new middleware pattern), stop and say
  so rather than working around it silently.

## Definition of done

`docker compose run --rm migrate up` on a clean database, then a single test
run exercises the full section 11 sequence against real Postgres/Redis and
real Anthropic/OpenAI calls, and passes. `go vet ./...` and
`go test ./... -race` are clean. Nothing in the "explicitly out of scope"
list above has been touched.

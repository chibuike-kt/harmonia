# ADR-003: Realtime layer via SSE + in-process hub; frontend stack

**Status:** Accepted
**Date:** 2026-09-05

## Context

No UI exists yet. Every route in the API is request-response; nothing
pushes state to a browser. The typing-bubble/presence conversation from
earlier in this project's design surfaced two gaps: (1) there's no
mechanism to notify a connected client that something changed, and
(2) `agents.status` has existed since the Milestone 1 schema but nothing
has ever transitioned it — `Claim`/`Complete` update `tasks.status` only.

## Decisions

**Transport: Server-Sent Events, not WebSocket.** Every real-time need
right now is one-directional (server → browser). SSE gets that with
plain HTTP and automatic client-side reconnection (`EventSource`), no
upgrade handshake. WebSocket becomes justified the moment real-time
bidirectional communication exists — concretely, when the deferred
`messages`/chat table gets built. Not before.

**Fan-out: an in-process hub, not Redis Pub/Sub.** A single Go process
serves this API today; an in-memory hub keyed by room ID (subscribe,
unsubscribe, publish, mutex-protected) is sufficient and simpler than
standing up distributed pub/sub for one instance. Redis Pub/Sub is the
correct upgrade the moment horizontal scaling is real — flagged below
under "Revisit When," not built preemptively.

**What flows through the hub, and what doesn't.** Two distinct kinds of
message:
- **Recorded events** — the exact `protocol.Envelope` payload already
  built for `event.Store.Record` in each handler, published to the hub
  *after* the enclosing transaction commits, never before. Publishing
  before commit would let a subscriber see a task transition that a
  failed transaction then rolls back — the same class of bug the
  transactional task+event work fixed for the audit trail itself, just
  one layer further out.
- **Ephemeral presence** — an agent's status transition
  (`available`/`running`). This does **not** go into the append-only
  `events` table; it's exactly the kind of high-churn, non-authoritative
  state that table was designed to exclude. It's broadcast over the hub
  live, and mirrored into a short-lived Redis key
  (`agent:{id}:status`) so a client connecting mid-session can fetch a
  current snapshot instead of only seeing future changes.

**`agents.status` transitions, finally wired.** `Claim` sets the task's
owner agent to `running`; `Complete` sets it back to `available`. Both
happen in the same transaction as the task write already does — one more
write inside an existing transaction boundary, not a new one.

**Frontend: Next.js, TypeScript, Tailwind, in this repo under `apps/web/`.**
Matches the stack the original product overview recommended, and
Kingsley's own existing strength (senior-level Node.js/TypeScript work).
Same repo as the Go backend — one product, two independent build systems
living as sibling directories; nothing about Go modules or `npm`
workspaces requires separating them, and keeping them together avoids
cross-repo version drift while both are moving fast. Per `CONTRIBUTING.md`'s
existing rule that a second language enters "only behind an ADR" — this
is that ADR for TypeScript, the same way ADR-001 was for the (rejected,
for now) Rust question.

## Revisit When

More than one server process needs to share room subscriptions — that's
the Redis Pub/Sub migration, not a redesign, since the hub's
`Publish`/`Subscribe` interface doesn't need to change, only its backing
implementation. Real-time bidirectional chat is the trigger for
reconsidering WebSocket.

# Phase 3 Build Brief — Realtime Layer + First Real UI — for Claude Code

**Read first:** `CLAUDE.md`, `CONTRIBUTING.md`, `docs/adr/ADR-001-language-decision.md`,
`docs/adr/ADR-002-user-auth-and-byok-credentials.md`,
`docs/adr/ADR-003-realtime-and-frontend-foundations.md`,
`docs/milestone-1-design.md`. ADR-003 is the source of truth for every
decision below.

## Objective

Make Harmonia watchable. Right now nothing pushes state to a browser and
no browser UI exists at all. This phase closes both gaps: a real-time
event stream from the backend, and the first four screens a human
actually uses (login, connect agents, create room, watch a room).

## Two batches — stop between them

Steps 1-4 are backend (Go, existing repo root). Steps 5-8 are frontend
(`apps/web/`, new). The frontend has nothing meaningful to build against
until step 4's SSE endpoint exists — **stop and report after step 4,
wait for review, before starting step 5.** This mirrors how Milestone 1
paused before its acceptance test and Phase 2 paused before BYOK wiring.

## What already exists — do not redesign it

- The full task/handoff/event transaction pattern from Milestone 1 and
  Phase 2 — `pool.Begin`/`defer rollback()`/`Commit`, `store.Querier`/
  `store.Beginner`. The realtime publish call is one more thing that
  happens after that pattern's `Commit`, not a change to the pattern
  itself.
- `internal/protocol.Envelope` — reuse it as the payload shape published
  to the hub. Don't invent a second message envelope format.
- `user.Authenticate`, `agent.RequireRoom`'s room-ownership pattern —
  the SSE endpoint's auth should look like every other session-authed,
  room-scoped route already in this API, not like something new.
- Redis, already wired via `internal/store` — the presence key described
  below is one more key in the same Redis, not a new dependency.

## What's explicitly out of scope — do not build any of this now

WebSocket, Redis Pub/Sub, the `messages`/chat table, multi-instance
deployment of any kind, typed/rich presence states beyond
`available`/`running` (no "thinking," "searching," etc. — there's no
multi-step agent work yet to report on honestly, per the earlier
discussion about not animating states that don't exist), any frontend
screen beyond the four named below.

## Build order — backend (steps 1-4)

1. **`feat(realtime): in-process hub`**
   New `internal/realtime` package. `Hub` type: thread-safe, keyed by
   `room_id`, `Subscribe(roomID) (<-chan Message, func())` returning a
   channel and an unsubscribe function, `Publish(roomID, Message)`.
   `Message` wraps either a `protocol.Envelope` (recorded event) or a
   lightweight presence payload (`agent_id`, `status`) — a small tagged
   type, not two unrelated structs jammed together. Unit tests: publish
   with no subscribers doesn't block or panic, multiple subscribers to
   the same room all receive a publish, unsubscribing stops delivery and
   doesn't leak the channel.

2. **`feat(realtime): publish events after transaction commit`**
   In `task/http.go` and `handoff/http.go`'s handlers, after each
   `tx.Commit()` succeeds, call `realtime.Publish(roomID, envelope)`
   with the same `protocol.Envelope` already built for
   `event.Store.Record`. This must happen strictly after commit, never
   before or inside the transaction — a rolled-back write must never
   reach a subscriber. Add a test that proves this ordering: force a
   post-commit publish failure path (or verify via a fake hub) and
   confirm a rolled-back transaction never calls `Publish` at all.

3. **`feat(agent): status transitions on claim/complete`**
   Extend the existing transactions in `task.Store.Claim` and
   `task.Store.Complete` (or their call sites — your call which is
   cleaner, note which) to also update `agents.status`: `running` on
   claim, `available` on complete. Same transaction, not a second one.
   After commit, publish the presence message via the hub, and mirror
   the new status into a Redis key (`agent:{id}:status`, short TTL — a
   day is plenty, this is a live-session convenience, not durable state).

4. **`feat(realtime): SSE endpoint`**
   `GET /v1/rooms/{room_id}/stream` — `user.Authenticate` +
   room-ownership check, same 404/403 pattern as agent registration. On
   connect: write an initial snapshot (recent events via
   `event.Store.ListByRoom`, current statuses for every agent in the
   room read from their Redis keys), then `hub.Subscribe` and stream
   each `Message` as an SSE event until the client disconnects. Handle
   client disconnect via request context cancellation — unsubscribe
   cleanly, no goroutine leak. Write a test that opens a stream,
   disconnects, and confirms the hub's subscriber count drops back to
   zero — a leak here is a slow, silent server-killer, worth catching
   now rather than under real load later.

**Stop here. Report on steps 1-4. Wait for review before step 5.**

## Build order — frontend (steps 5-8)

5. **`feat(web): Next.js scaffold`**
   `apps/web/` — Next.js, TypeScript, Tailwind, per ADR-003. Minimal:
   app shell, a typed API client wrapping `fetch` against the Go
   backend's base URL (env-configured, not hardcoded), linting
   (ESLint) and a format check, wired into CI as its own job alongside
   the existing Go one — don't let the two toolchains' checks block on
   each other, but both need to run.

6. **`feat(web): login screen`**
   GitHub and Google OAuth buttons, redirecting to the existing
   `/v1/auth/{provider}/login` routes. Google's button exists and is
   wired identically to GitHub's — it simply won't complete until
   `GOOGLE_CLIENT_ID`/`SECRET` exist, same honest-incompleteness pattern
   the backend itself has used throughout (state plainly in your report
   that Google's button is structurally present, not live-verified,
   pending the same prerequisite already tracked in ADR-002).

7. **`feat(web): connect-agents screen`**
   Provider cards driven by a small config list (not hardcoded to
   exactly two — per the earlier product discussion about staying open
   to more providers), each wired to `POST /v1/credentials`,
   `GET /v1/credentials` for current state, `DELETE /v1/credentials/{provider}`
   to disconnect. Reflect verified/failed state from the API's own
   response — don't add client-side validation logic that duplicates
   what `Connect`'s verify-before-save already does server-side.

8. **`feat(web): create-room and room-view screens`**
   Single-input room creation (`POST /v1/rooms`), landing in a room view
   that opens the SSE stream (`EventSource` against step 4's endpoint)
   and renders incoming events as they arrive — task cards, handoff
   cards, agent presence indicators, visually distinct from each other
   per the earlier UI discussion. No animation library integration yet
   (`thinking-orbs` etc.) — that's a follow-up once this raw plumbing is
   proven to work; get real events rendering first, make them pretty
   after.

## Constraints that apply throughout

- Follow `CLAUDE.md`/`CONTRIBUTING.md` — atomic commits, stage and stop,
  no AI attribution.
- The SSE endpoint requires the same auth discipline as every other
  route in this API — don't let "it's just a stream" be a reason to
  skip the ownership check.
- Frontend code doesn't reimplement backend validation — it reflects
  what the API says, and handles the API's actual error shapes
  (`{"error": "..."}`, established back in Milestone 1's step 2).

## Definition of done

A user can log in with GitHub, connect an OpenAI credential, create a
room, and watch task/handoff events and agent presence update live in
the browser without refreshing — driven by real backend state changes,
not mocked data. `go vet`/`go test` clean on the backend; lint/typecheck
clean on the frontend; both CI jobs green. Google OAuth and any provider
beyond Anthropic/OpenAI remain explicitly structural-only, same as
established throughout this project.

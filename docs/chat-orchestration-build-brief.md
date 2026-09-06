# Conversational chat + first orchestration path — for Claude Code

**Read first:** `CLAUDE.md`, `CONTRIBUTING.md`,
`docs/adr/ADR-004-conversational-chat-and-first-orchestration.md`,
`docs/milestone-1-design.md`. ADR-004 is the source of truth for every
decision below.

## Objective

A human in a room can @mention an agent and get a real, generated reply
— the first time anything in this system calls a provider on an agent's
behalf outside a test harness. `credentials.Store.Resolve` finally gets
a production caller.

## Two batches — stop between them, same rhythm as every prior phase

Steps 1-4 are backend. Steps 5-8 are frontend. Frontend has nothing real
to build against until the invocation path actually works — stop and
report after step 4.

## What already exists — reuse, don't rebuild

- `migrations/0006_messages.up/down.sql` — schema for this phase, already
  written per ADR-004.
- `internal/realtime` — the hub and `Message` tagged union. Add a new
  kind for chat messages; don't invent a second pub/sub mechanism.
- `credentials.Store.Resolve` — this is the whole reason it exists. Use
  it exactly as built; fall back to the env-var dev path only as already
  established.
- `task.Store.SetStatus` (via `agent.Store`) — reuse for the
  running/available typing signal. No new status value.
- The transactional pattern (`store.Querier`/`Beginner`,
  publish-after-commit) — the message write is one more thing using it,
  not a new pattern.

## What's explicitly out of scope — do not build any of this now

Chat triggering task/handoff creation, multi-agent turn-taking beyond
"whoever's mentioned responds," streaming token-by-token responses
(post the complete reply when generation finishes, not partial tokens),
message editing/deletion, threads, reactions. All real, all later.

## Build order — backend

1. **`feat(message): internal/message package + migration applied`**
   `Store` with `Create` (human message) and `ListByRoom` (recency
   window, capped — pick a reasonable limit like 50 and note it).
   Confirm the migration applies cleanly.

2. **`feat(message): POST /v1/rooms/{room_id}/messages`**
   Human posts `{content, mentioned_agent_id?}`. Validates the mentioned
   agent (if present) actually belongs to this room — same non-leaking
   404/403 pattern as every other room-scoped resource. Transactional
   write + publish-after-commit over the hub, reusing the existing
   handler pattern exactly.

3. **`feat(orchestration): async agent invocation on @mention`**
   This is the core of the phase. After the human message commits and
   publishes, if `mentioned_agent_id` is set, launch the response
   generation asynchronously (a goroutine is fine — there's no job queue
   in this codebase and this doesn't need one yet):
   - Set the mentioned agent's status to `running`, publish presence
     (same mechanism as task claim).
   - Resolve its provider client via `credentials.Store.Resolve`
     (owner's BYOK credential, env-var fallback per existing pattern).
   - Build context: the last N messages in the room (from step 1's
     `ListByRoom`), formatted as a conversation for `Generate`.
   - On success: insert a new `agent`-authored message with the reply,
     setting `reply_to_message_id` to the triggering (mentioning)
     message's id — this is what lets the UI show "replying to X" when
     the conversation has moved on before the reply arrives. Publish it
     over the hub.
   - On failure: insert a visible message explaining the failure (not a
     silent drop) — state what you chose for its content/framing.
   - Either way: set status back to `available`, publish presence.

   Write this so a request handler launching a goroutine doesn't leak
   or panic silently — if the goroutine can outlive the request, make
   sure a panic inside it doesn't crash the process (recover, log,
   still flip status back to `available` and post a failure message).

   **Verify the self-referencing FK survives the room-delete cascade.**
   `reply_to_message_id` references `messages(id)` with no explicit
   `ON DELETE` action — this should be fine because all of a room's
   messages are removed together in one statement inside
   `delete_room_cascade`, and Postgres checks FK constraints at
   statement end, not row-by-row. "Should be fine" isn't good enough on
   its own given this project's history with permission/constraint
   surprises — write a real integration test: seed a room with a
   mention-and-reply pair (so the FK is populated), call
   `delete_room_cascade`, confirm it succeeds and both rows are gone.

4. **`feat(realtime): extend the SSE snapshot with recent messages`**
   The stream's initial snapshot (already includes recent events and
   agent presence) should also include recent messages, so a client
   connecting mid-conversation sees history, not just future messages.
   No new endpoint — this is the same snapshot-then-subscribe pattern
   already built, extended.

**Stop here. Report on steps 1-4, including a real end-to-end proof: a
human message with a real @mention actually produces a real generated
reply from a real provider call, not just that the code compiles.**

## Build order — frontend

5. **`feat(web): message composer`** — text input + send, plus an
   @-picker listing the room's agents, sending the structured
   `{content, mentioned_agent_id}` shape from step 2. No fragile
   client-side text parsing either.

   Include a `+` menu next to the composer with **honestly labeled,
   currently-inert** items: "Search the web," "Add a file." These are
   real, near-term follow-up work (see ADR-004's addendum) — both
   providers support them as server-side tools, this phase just isn't
   building the wiring yet. Label them plainly rather than leaving the
   `+` mysteriously empty or inventing vague "coming soon" copy — the
   person should know exactly what's coming, not guess.

6. **`feat(web): render messages in the room timeline`** — interleaved
   chronologically with the existing task/handoff/event cards, visually
   distinct as chat bubbles (human vs. agent), matching the three-grammar
   principle already established for this room view.

   When a message has `reply_to_message_id` set, show a compact "replying
   to" quote referencing that message — **but only when it isn't the
   immediately preceding message in the timeline.** If the agent replied
   right after being mentioned, the tag is just noise; it earns its place
   specifically when the conversation moved on before the reply landed.

7. **`feat(web): typing indicator`** — when the mentioned agent's
   presence flips to `running` (already streamed live), show a real
   indicator near where the reply will land; replace it with the actual
   message on arrival. This is the first genuine use of `ThinkingOrb`'s
   `working` state tied to real in-flight work rather than a static
   presence dot.

8. **Failure messages render as a distinguishable style** — not
   identical to a normal agent reply, not alarming either; your call on
   the exact treatment, state what you chose.

9. **`feat(web): side panel for code/document content in a reply`**
   A lightweight version of the original product doc's Artifact System
   (section 60) — not the full versioned/hashed backend it describes.
   If an agent's message contains a fenced code block (or similarly
   substantial standalone content), render a small chip/reference for
   it inline in the chat bubble instead of the full block; clicking it
   opens a slide-out side panel with the content properly rendered
   (syntax highlighting for code). No new storage — the message row
   already persists the content durably, so reopening the panel later
   just re-reads the same message. Closing the panel returns to the
   normal chat view. Scope this to rendering only; don't build editing,
   versioning, or a separate artifacts table — that's real, deferred,
   bigger work if it's ever needed.

## Definition of done

A human in a room with one connected agent types a message @mentioning
it, sees the message appear instantly, sees a real typing indicator, and
within a real few seconds sees a genuine LLM-generated reply — driven by
an actual provider call through `Resolve`, not a fixture. Task/handoff
cards from prior phases still render correctly in the same timeline. A
reply containing a fenced code block offers a working side panel view.
The `+` menu's two items are visible and honestly labeled as not-yet-
wired, not fake-functional. `go vet`/`test` and frontend lint/typecheck/
build all clean.

## Addendum (2026-09-06): fixes and additions found after steps 1-9 landed

Two small frontend fixes, already real bugs, not new scope:

10. **Hide the timeline's scrollbar** — same technique as the mockup's
    `.no-scrollbar` class (`scrollbar-width: none` plus
    `::-webkit-scrollbar { display: none }`). Scrolling still works,
    just no visible track/thumb.

11. **"New messages" scroll-to-latest button** — a small centered pill
    above the composer, shown whenever the user isn't at the bottom of
    the timeline (scrolled up, or a new message arrived while scrolled
    up), hidden when already at the bottom. Clicking it scrolls smoothly
    to the latest entry.

One real feature, per ADR-004's new addendum — room creation without a
name, auto-titled from the first message:

12. **Backend: `POST /v1/rooms`'s `name` becomes optional.** Omitted or
    empty defaults to the literal placeholder `"New room"`. Creation
    responds immediately as before — no other behavior changes here.

13. **Backend: async auto-title job.** After the first message in a room
    is stored (reuse the existing publish-after-commit hook point — this
    fires once per room, on whichever message lands first, not on every
    message), resolve one of the room owner's connected BYOK credentials
    (platform-level, not tied to a room-scoped agent — a fresh room may
    have none registered yet; your call which provider to prefer if the
    owner has more than one connected, state what you chose), generate a
    short title from that message's content, then update the room's name
    — **but only if it's still the placeholder.** Re-read the current
    name immediately before writing; if a human already renamed it, skip
    silently. This guards a real race between a slow generation job and
    a fast manual rename. Publish the new name over the realtime hub (a
    new message kind) so the sidebar and header update live.

14. **Frontend: remove the `/rooms/new` naming form entirely.** Clicking
    "New room" in the sidebar calls the create endpoint directly (no
    name) and navigates straight into the new room — no intermediate
    page. When the generated title arrives live, reveal it with a
    simulated character-by-character typing animation over the complete
    string already returned — not real token streaming, consistent with
    how reply generation already works.

15. **Date dividers** in the timeline, grouping entries by calendar day
    — small, standard, frontend-only.

**Explicitly not in this addendum** — the room decisions/info panel, a
real cost/token indicator, and the approval-required card type. Each
needs its own design pass before it's buildable (see ADR-004's second
new addendum for why); none are scope here.

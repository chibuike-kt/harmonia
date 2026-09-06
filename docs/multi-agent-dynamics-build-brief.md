# Multi-agent group dynamics — for Claude Code

**Read first:** `docs/adr/ADR-006-multi-agent-group-dynamics.md`,
`migrations/0011_multi_agent_dynamics.up/down.sql` (already written,
confirm it applies cleanly against the existing `messages`/`rooms`
data — the backfill step matters, don't skip verifying it).

## Three batches, hard stop between each

These have genuinely different risk profiles — A is safe and additive,
B introduces real loop/cost risk, C executes real actions on an agent's
own initiative. Reviewing them together would mean the riskiest one
gets the least scrutiny. Stop after each.

## Batch A — multi-mention

1. **Migration + backfill** — confirm `message_mentions` is correctly
   populated from every existing `mentioned_agent_id` before that column
   drops. Write a test that seeds a message with a mention, runs the
   migration path, and confirms the join-table row exists with the
   right agent — don't just trust the `INSERT ... SELECT` compiles.

2. **`POST /v1/rooms/{room_id}/messages` accepts `mentioned_agent_ids`**
   (plural, array) instead of the old singular field. Validate every
   mentioned agent belongs to the room, same non-leaking pattern as
   always — if any one is invalid, decide whether the whole request
   fails or valid ones still proceed, and state which you chose.

3. **One async invocation per mentioned agent**, each independently
   following the existing orchestration path (status, resolve
   credential, generate, publish), each reply's `reply_to_message_id`
   pointing at the one human message that mentioned it.

4. **Frontend: the @-picker allows selecting more than one agent** per
   message, chips for each, matching the existing single-mention chip
   pattern extended to multiple.

**Stop. Report with a real proof: one message mentioning two agents,
two independent real replies arriving.**

## Batch B — agent-to-agent cascading

5. **`rooms.agent_cascading_enabled`** — a toggle in the room info
   panel, off by default per ADR-006. Only relevant once on.

6. **Detecting a mention inside an agent's own generated reply.** The
   model's response needs a way to express "I'm mentioning Agent X" —
   this should go through the same structured mechanism as human
   mentions, not text-scanning the reply for `@Name` (same reasoning
   ADR-004 already gave against parsing human messages that way). The
   cleanest approach: give the model a lightweight structured way to
   express a mention as part of its tool-use surface (batch C builds
   real tools right after this — consider whether mention-as-a-tool-call
   is more consistent than a separate mechanism, and say which you chose
   and why).

7. **The cascade depth counter — in-memory, threaded through the call
   chain, not persisted.** Default cap 3. When triggering agent N+1 from
   agent N's reply, check cascading is enabled for the room AND the
   depth is under the cap before invoking. At the cap, post a plain
   visible message stating the cascade stopped and a human should
   continue — never a silent stop.

8. **Write the loop test deterministically, not with a timeout.** Two
   agents configured to always mention each other back — confirm the
   chain stops at exactly the configured depth, every run, not
   "usually." Same discipline as the earlier race-guard test that forced
   its collision synchronously rather than trusting a sleep.

**Stop. Report with real proof: cascading disabled by default (confirm
a mention-in-a-reply does nothing when off), then enabled with a real
multi-hop exchange that stops exactly at the cap.**

## Batch C — agent-initiated actions

9. **`create_task` and `request_handoff` as real provider tool
   definitions** — Anthropic's `tool_use`, OpenAI's function calling.
   Both providers' clients need this; note if one requires more
   structural change than the other, same as the Chat-Completions-vs-
   Responses-API gap already known for OpenAI's search tool.

10. **`create_task` executes immediately** against the existing
    `task.Store.Create` path — same effect as a human hitting the API
    directly, no new gate.

11. **`request_handoff` creates an `agent_action_proposals` row,
    status `pending` — does not call `handoff.Store.Request`.** Publish
    it over the hub so the approval card (built with nothing to trigger
    it two rounds ago) finally renders for real. Approving it executes
    the stored payload against the real handoff path; rejecting just
    marks it resolved. Confirm the existing `handoffs.status` state
    machine (REQUESTED/ACCEPTED/REJECTED, the receiving agent's own
    response) is untouched by this — these are two different questions,
    per ADR-006, and the code should reflect that distinction clearly,
    not blur them.

**Stop. Report with real proof: an agent, mid-conversation, deciding on
its own to create a task (appears immediately, same as always) and to
request a handoff (produces a real, clickable approval card; approving
it produces a real handoff exactly as if a human had requested it).**

## Constraints across all three batches

- Every new table gets its own grant in its own migration — the
  `0005`/messages-grants lesson applies to every future table, always
  check, never assume the blanket grant from `0005` covers it.
- Cost is real here in a way it wasn't for single-reply chat — batches
  B and C both meaningfully increase how many API calls a single human
  action can trigger. Nothing here should make a call the human didn't,
  directly or through a bounded, visible chain, cause.

## Definition of done

A human can address two agents in one message and get two real replies.
A room with cascading enabled shows two agents genuinely conversing
with each other, capped and visibly stopped at the limit. An agent
decides on its own to create a task (visible immediately) and to
request a handoff (visible as a real, actionable approval card) — the
room finally behaves like the collaboration space the original product
document described, not a single-agent chatbot with an @ symbol.

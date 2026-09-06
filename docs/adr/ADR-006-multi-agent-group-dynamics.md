# ADR-006: Multi-agent group dynamics — multi-mention, cascading, agent-initiated actions

**Status:** Accepted
**Date:** 2026-09-06

## Context

ADR-004 deliberately scoped chat to a 1:1 loop, naming its own limits
explicitly: "multi-agent rooms need real turn-taking beyond 'whoever's
mentioned responds'" and "chat does not trigger structured actions yet"
were both flagged as real, later work under "Revisit When." This is
that phase — the room feeling like a genuine multi-agent collaboration
space rather than a single-agent Q&A box with an @ symbol in front of
it.

## Decisions

**A — One message, many agents.** A human message can address more
than one agent. `messages.mentioned_agent_id` (singular) is replaced by
a `message_mentions(message_id, agent_id)` join table — the message is
stored once, one async invocation fires per mentioned agent, each
producing its own reply with `reply_to_message_id` pointing at the
single triggering message. No new risk here: still exactly as many
replies as explicit mentions, still entirely human-triggered.

**B — Agent-to-agent cascading, opt-in with a hard cap, both required.**
An agent's own generated reply can mention another agent, triggering it
automatically — this is genuinely new risk, not a variation on existing
risk: two agents capable of mentioning each other is a real infinite-
loop shape, and every hop is a real charge against someone's own BYOK
key. Two independent guards:
- **Opt-in per room** (`rooms.agent_cascading_enabled`, default
  `false`) — a room doesn't cascade unless a human deliberately turns
  it on.
- **A hard cap on chain depth** (default 3 hops), enforced regardless
  of the opt-in setting, tracked per-invocation through the
  orchestration call (an in-memory counter passed along the chain, not
  persisted room state — it describes one cascade's progress, not the
  room). When the cap is hit, the chain stops and a plain, visible
  message says so — never a silent truncation.

Either guard alone is insufficient: opt-in without a cap doesn't stop a
real loop once someone's turned it on; a cap without opt-in means every
room gets surprise multi-hop spend by default.

**C — Agents get two real tools, native function-calling, not text
parsing.** `create_task` and `request_handoff`, defined via each
provider's actual tool-use API (Anthropic's `tool_use`, OpenAI's
function calling) — the model decides to call one, the platform
executes it, exactly the "native provider capability, not something
Harmonia reimplements" principle already established for the deferred
web-search work.

- **`create_task` executes immediately** — same effect as a human
  calling `POST /v1/tasks` directly. Low-stakes, already a frequent,
  ordinary operation in this system.
- **`request_handoff` does not execute immediately.** It's recorded as
  a pending, human-approvable proposal and only fires the real
  `handoff.Store.Request` on explicit approval. This is genuinely
  different from `handoffs.status`'s existing `REQUESTED`/`ACCEPTED`/
  `REJECTED` states, which represent the *receiving* agent's response
  to an already-made request — not whether the request should have been
  made at all. Conflating the two would be modeling two different
  questions as one. A new table (`agent_action_proposals` — room_id,
  proposing_agent_id, action_type, payload jsonb, status, created_at)
  holds the pending state; approval executes the stored payload against
  the real, existing handoff-creation path, no new execution logic.
- **This is the first real trigger for the approval-required card**
  built with no wiring behind it — it finally has something genuine to
  approve.

## Revisit When

Agents proposing actions beyond these two (the tool-execution phase
proper — search, files, anything with a larger trust boundary) — that's
further scope than this ADR covers, same "don't build ahead of what's
needed" discipline as everywhere else in this project.

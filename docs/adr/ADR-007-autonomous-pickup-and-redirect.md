# ADR-007: Autonomous message pickup and busy-agent redirection

**Status:** Accepted
**Date:** 2026-09-07

## Context

Real use of multi-agent rooms surfaced a gap neither ADR-004 nor
ADR-006 covered: a human group chat doesn't require explicitly
addressing someone by name for a relevant message to get picked up, and
if the person you asked is busy, someone else often covers and says so.
This ADR extends chat beyond both prior decisions — and needs real new
cost guardrails, because it introduces the largest spend surface yet:
instead of a human choosing to spend on one specific agent, every agent
gets a chance to look at every unaddressed message.

## Decisions

**Two genuinely separate capabilities, gated separately, because their
cost profiles are fundamentally different.**

**A — Busy-agent redirect on an explicit mention.** If a mentioned
agent's status is already `running`, it may redirect to another
available agent via the same `mention_agent` tool-call mechanism
already built for cascading (ADR-006, Batch B) — same opt-in toggle,
same hard depth cap. The redirect must be a real, visible message
("ChatGPT is already working on this, so I'll take it") — never a
silent swap. This reuses proven infrastructure and adds no new spend
trigger: the mention already implied a human was willing to spend on a
reply, this just reroutes it to whoever can actually answer.

**B — Autonomous pickup of unaddressed messages.** Per explicit product
decision, every agent in the room evaluates every unaddressed message —
not one designated agent. Gated behind its own toggle,
`rooms.autonomous_pickup_enabled`, independent of cascading's toggle.
This is a materially different cost surface than A: A only spends money
when an agent was already going to act; B spends money evaluating every
single message whether or not anyone ends up responding.

**B is two-phase, cost-shaped:**
- **Phase 1, cheap:** each agent gets a minimal classification call —
  not a full reply — asking whether this message needs its response,
  and whether it reads as a single actionable request or something
  worth multiple perspectives on. Short prompt, short expected output,
  the cheapest model a provider offers for this kind of call if one
  exists, distinct from whatever model the agent normally uses for real
  replies.
- **Phase 2, full generation, only for whoever actually claims it.**
  Default to single-claim — atomic, first agent's "yes" wins, the exact
  pattern `task.Store.Claim`'s `WHERE status = 'QUEUED'` guard already
  proves safe under real concurrency. Messages multiple agents flag as
  genuinely worth several views are the harder case — start
  conservative (single-claim unless there's a clear, cheap signal
  otherwise) and revisit once real usage exists to reason from, not
  before.

**Cost safeguards, always on when B is enabled:** skip evaluation
entirely for short, low-content messages (acknowledgements like "ok,"
"thanks") — these are never going to be a real task. Apply a minimum
interval between an agent's own consecutive evaluation calls, so a
rapid burst of ordinary conversation doesn't multiply Phase 1 cost
linearly with message count.

**Both toggles default to off, without exception.** Given real dollars
on someone's own key, defaulting either on would contradict the same
BYOK-transparency principle that's driven the cost pill and the trust
copy throughout this build.

## Revisit When

A reliable, cheap signal exists for genuinely distinguishing "needs one
responder" from "needs several" — until then, single-claim is the safe
default even though it under-serves the "multiple perspectives" case
described in the original request.

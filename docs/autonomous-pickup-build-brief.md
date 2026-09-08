# Autonomous pickup and busy-agent redirection — for Claude Code

**Read first:** `docs/adr/ADR-007-autonomous-pickup-and-redirect.md`.
This extends both ADR-004's mention rule and ADR-006's cascading —
re-read those two if the reasoning here doesn't immediately click.

## Two batches, hard stop between — capability A is far cheaper and safer than B

## Batch A — busy-agent redirect on explicit mention

1. When an agent is invoked via a real mention and its own status is
   already `running` at invocation time, allow it to call
   `mention_agent` targeting a free agent instead of generating a reply
   itself — same tool, same cascading opt-in, same depth cap already
   built and proven in ADR-006's Batch B. Don't build a second mechanism.

2. The redirect must produce a real, visible message narrating it
   ("ChatGPT is already working on this, so I'll take it") before the
   redirected agent's own reply — never a silent handoff a human can't
   see happened.

**Stop. Report with real proof: mention an agent while it's genuinely
mid-generation on something else, confirm the redirect fires, is
visible, and counts against the existing depth cap correctly.**

## Batch B — autonomous pickup of unaddressed messages

3. **`rooms.autonomous_pickup_enabled`** — new column, own toggle in the
   room info panel, off by default, independent of the cascading toggle.

4. **Phase 1 — cheap classification, every agent, every unaddressed
   message, only when the toggle is on.** Define this as a distinct,
   minimal call — not a reuse of the full reply-generation path. Skip it
   entirely for short/low-content messages (a simple length or
   acknowledgement-pattern check is fine, state what you used). Use the
   cheapest model each provider offers if one exists; if a provider has
   no meaningfully cheaper tier, say so plainly rather than guessing.

5. **Phase 2 — real generation, single-claim, atomic.** An agent that
   phase 1 flagged "yes" attempts an atomic claim on the message before
   generating — same `WHERE status = 'QUEUED'`-style conditional write
   as `task.Store.Claim`, applied here to prevent two agents that both
   said yes from both generating a full reply. Write the deterministic
   concurrency test this pattern always gets in this codebase: many
   agents, one message, exactly one claim succeeds — every run, not
   usually.

6. **Rate-limit phase 1 itself** — a minimum interval between one
   agent's own consecutive evaluation calls, so a fast burst of ordinary
   messages doesn't linearly multiply cost. State the interval you
   chose and why.

7. **Cost visibility:** phase 1's classification calls are real spend —
   make sure they're captured by the existing token/cost tracking
   (messages.input_tokens/output_tokens) the same as any other real
   call, not silently excluded from the cost pill just because they're
   "just" a classification.

**Stop. Report with real proof: a multi-agent room, pickup enabled, an
unaddressed message that reads as a task — confirm multiple agents'
phase-1 calls happen, exactly one claims and generates a full reply, and
the cost pill reflects every real call including the phase-1 ones. Also
confirm pickup does nothing at all when the toggle is off — same
suppression-proof discipline as cascading's disabled-by-default test.**

## Constraints across both batches

- Never let a phase-1 evaluation itself become a full-cost call by
  accident — this is the entire point of the two-phase design, and it's
  worth a direct cost comparison in your report (phase 1's actual token
  cost vs. a normal reply's) to prove the distinction is real, not just
  named.
- Both toggles stay off by default, no exceptions, per the ADR.

## Definition of done

A busy agent redirects a mention visibly and correctly, bounded by the
existing cascade cap. A room with pickup enabled has multiple agents
evaluate an unaddressed message cheaply, exactly one responds fully, and
the real cost of both phases is visible and accurate. Both behaviors do
nothing when their respective toggles are off.

# Sustained agentic loops — for Claude Code

**Read first:** `docs/adr/ADR-011-sustained-agentic-loops.md` in full,
and re-read ADR-010's presence-gate reasoning before starting — this
extends it, doesn't replace it.

## Two batches, hard stop between — the loop mechanism first, delegation exception second

## Batch A — the loop itself

1. **Session start requires three real numbers before anything runs**
   — max tool-call cycles, a dollar cap, a wall-clock limit. Build the
   real UI for this (a form or modal in the IDE, triggered from the
   terminal when a human gives a task that warrants a sustained
   session rather than a one-shot reply — your call on the exact
   trigger UX, state what you chose). No loop starts without all three
   set.

2. **The orchestration loop**: given a task and the three bounds, the
   agent cycles through tool-call → observe result → decide next step
   → tool-call again, reusing every existing IDE tool unchanged. Each
   cycle checks, before acting: is presence still active, is the
   iteration count under the cap, is the dollar spend under the cap,
   is the wall-clock limit not exceeded. Any one failing stops the
   loop with the correct real behavior per the ADR — silent and
   immediate for presence loss, a clear visible message for any
   configured bound being hit.

3. **A real, always-visible Stop control** while a loop is running,
   distinct from presence loss — a human explicitly ending a session
   that's still being watched.

4. **Self-verification**: the loop's framing should expect the agent to
   run real build/test/lint commands before declaring completion, not
   just assert it's done. State how you built this expectation into
   the prompting.

5. **Every cycle renders in the terminal** using the existing narration
   pattern — reuse it exactly, don't build a second transcript view.
   Cost pill reflects real running spend against the session's cap
   live.

**Stop. Report with real, live proof: a real bounded session, a real
multi-step task actually completing with real verification, and
separately, a real proof that losing presence mid-loop stops it
immediately — same rigor as the original presence-loss proof from the
companion safety work.**

## Batch B — in-loop delegation exception

6. **Delegation to another agent during an active, presence-gated loop
   executes immediately** — no approval card, per the ADR's explicit,
   narrow exception. Confirm this only applies inside a live session
   with active presence; any handoff proposed outside that context
   (the existing async path) must still go through the full
   `request_handoff` approval flow exactly as before. Write a test
   proving both paths are genuinely distinct, not that one quietly
   swallowed the other.

7. **The delegation is visible in the terminal live**, same as
   everything else in the loop — a human watching needs to see the
   handoff happen, not just infer it from a second agent suddenly
   acting.

**Stop. Report with real proof: an in-loop delegation happening live
with no approval click, and a separate async handoff (outside a live
session) still correctly requiring approval — both proven in the same
report so the boundary between them is demonstrably real.**

## Definition of done

A human can start a real, bounded, sustained working session in the
IDE. The agent works through many real steps, visibly, verifying its
own output before calling something done. The session is genuinely
supervised the whole time — presence loss stops it instantly, a
configured bound stops it visibly, a human can stop it directly. Agents
can delegate to each other live without friction inside that supervised
session, while the existing async approval flow remains fully intact
outside of it.

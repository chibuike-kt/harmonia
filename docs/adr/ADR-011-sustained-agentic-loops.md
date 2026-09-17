# ADR-011: Sustained autonomous agentic loops in the IDE

**Status:** Accepted
**Date:** 2026-09-17

## Context

Every agent in Harmonia today is invoked once per turn: read context,
produce one reply, maybe one tool call, stop. That's structurally
different from Claude Code, Codex, or Cursor's Agent mode, where the
model keeps working across many tool calls, observing its own results,
until a task is actually done. The gap users feel isn't a weaker
model behind a weaker key, it's the absence of everything built around
the model to let it work this way. This ADR closes that gap, scoped
deliberately narrow at first.

## Decisions

**Scoped to the IDE, not chat rooms.** A sustained loop only makes
sense where there's something real to act on across many steps — read
a file, run a test, observe the failure, fix it, run it again. Chat
rooms stay exactly as they are; this doesn't change how a conversational
reply works.

**The presence gate extends from a single action to a whole session.**
ADR-010 gated one edit or one command on a human actively watching.
Here, presence gates an entire autonomous working session — while a
human is present, an agent can cycle through many tool calls (read,
write, run, observe, decide, repeat) without a new human message
between each step. The moment presence is lost mid-loop, the loop stops
immediately — no grace period, no finishing the current step first.
Same zero-ambiguity principle already decided for the live cursor
vanishing instantly: an unsupervised loop isn't a degraded version of a
supervised one, it's a different, unacceptable thing.

**In-loop delegation between agents is immediate, not approval-gated —
but only inside an actively-watched live session.** When one agent
hands part of the work to another mid-loop, it executes immediately,
reusing the exact reasoning ADR-010 already established for live file
edits: the human's active presence during that session is itself the
safety gate. This is a deliberate, narrow exception to `request_handoff`'s
normal approval requirement, not a change to it — any handoff proposed
outside a live, presence-gated session (the existing async case ADR-006
already covers) still goes through the full approval card exactly as
before. The exception is scoped to "someone is right there watching it
happen," nothing broader.

**No hard default bound — the human sets real limits before a loop
starts.** A loop cannot begin without the user configuring three real
numbers: a maximum tool-call cycle count, a dollar cap, and a wall-clock
time limit. These aren't soft suggestions, they're required inputs to
starting a session at all. This keeps the actual risk tolerance a
deliberate, per-session human decision rather than a number this ADR
guesses at with no real usage data behind it yet.

**Self-verification before declaring done.** A loop is expected to run
the project's own build/test/lint commands (already available through
the companion's shell access) and observe a real pass before
considering a task complete — not just assert completion. This is
ordinary good engineering practice, not a novel technique, and it's
exactly the kind of thing the sustained loop makes possible that a
single-shot reply never could.

**Every cycle renders live in the terminal**, reusing the existing
narration pattern exactly — no new UI surface for this, the terminal
that already shows agent narration and real commands is where a loop's
work is visible, step by step, as it happens.

**Real-time cost visibility against the configured cap**, extending the
existing cost pill rather than building a second display for this.

**Stopping conditions, all real, none silent:** the agent declares
completion (ideally after verification), a configured bound is hit
(clear, visible message, never a silent stop), presence is lost
(immediate, no message needed — this is the safety mechanism working
correctly, not a failure), or the human explicitly interrupts it via a
real, always-visible Stop control while a loop is running.

## What this reuses versus what's genuinely new

Reused, unchanged: every existing IDE tool (file read/write, shell
exec), `create_task` (still immediate, still low-stakes), the presence
signal itself, the terminal's narration rendering, the cost-tracking
mechanism. Genuinely new: the loop orchestration pattern itself (many
cycles under one invocation instead of one-shot), the in-loop
delegation exception, and the required-bounds-before-start UI.

## Revisit When

Real usage data exists to reason about whether a sane default bound
would actually help most sessions rather than just adding friction —
until then, requiring an explicit, deliberate number from the human is
the safer default.

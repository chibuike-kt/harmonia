# ADR-012: Unattended autonomous sessions and concurrent multi-agent collaboration

**Status:** Accepted
**Date:** 2026-09-18
**Reverses, deliberately and explicitly:** ADR-010's presence
requirement, for a new session type only. Attended sessions (ADR-010,
ADR-011) are completely unchanged — this adds a second, higher-risk
mode alongside them, it doesn't replace the safer one.

## Context

Every prior IDE safety decision assumed a human watching in real time
was the actual safety mechanism, with git as a secondary net for file
history. This ADR removes that assumption for a genuinely unattended
session, on an explicit, informed decision, with the stated
consequence understood: a git-tracked file's changes are recoverable,
a destructive command's broader effects on the machine or network are
not, and there is no real-time human catching a bad decision as it
happens.

Given that, this ADR's whole job is building the strongest realistic
guardrails that can exist *without* a human in the loop — not
replicating presence's protection (impossible), but bounding the blast
radius of its absence.

## Decisions

**A real command allowlist for unattended mode specifically — not the
blocklist ADR-010 already rejected.** ADR-010's "no blocklist" reasoning
was about the impossibility of enumerating every bad command. An
allowlist is the opposite mechanism: enumerate the specific, known-safe
operations permitted (file read/write/create within the folder, real
build/test/lint commands, package-manager installs from real
registries), and nothing outside that list runs, full stop. This is
standard practice for genuinely unattended automation (the same
principle CI systems use) and it's a fundamentally stronger guarantee
than a blocklist, not a variation on one.

**Network access is restricted by default, not open.** Package-manager
installs from real, known registries (npm, pip, go get against their
real default registries) are allowlisted as a named exception, since
that's ordinary, necessary build activity. Arbitrary outbound requests
(curl, wget, anything hitting an arbitrary URL) are not permitted in
unattended mode. This is the single largest, least-visible risk of
unattended execution — data leaving the machine, or something malicious
being pulled in — and it gets the strictest default of anything in this
ADR.

**Absolute, zero-exception folder-scope enforcement.** Every file
operation is confined to literally inside the opened folder's own
directory tree — no `..` traversal, no absolute paths outside it, ever,
with no exception even for a command that would otherwise be
allowlisted. The existing path-traversal guard from the companion work
gets re-verified specifically against unattended mode, adversarially,
not assumed to already cover this case.

**A genuinely tighter, separate consent step for unattended mode.** The
existing one-time companion consent screen (ADR-010) covers attended
sessions. Starting an *unattended* session requires its own explicit,
separate consent, every time a new unattended session begins (not a
one-time global toggle) — stating plainly what's different this time:
no one will be watching, here's what's allowlisted, here's what isn't.

**Tighter default bounds for unattended sessions than attended ones.**
ADR-011's cycle/cost/time caps still apply and are still required
inputs — but unattended mode's suggested defaults should be
meaningfully more conservative than an attended session's, since there
is no human able to notice a session going somewhere unproductive and
stop it early. The human can still set them higher deliberately; the
default nudge should be caution.

**Full session transcript, durably logged, not just live-rendered.**
Every action an unattended session takes — every command, every file
change, every decision — is saved in full, reviewable after the fact.
This is the closest available substitute for the real-time watching
that's been removed: not prevention, but a complete, honest record.

**A real post-session summary is presented the next time the human
opens the IDE.** Not just raw git history to comb through — a genuine
digest of what happened while nobody was watching, surfaced
proactively, not something the human has to remember to go looking for.

**Concurrent multi-agent collaboration, scoped to different files
first.** Two agents working simultaneously on genuinely different
files in the same folder is architecturally simple — no real conflict,
since they're touching different resources. That's the default,
required shape for this version. Two agents editing the *same* file at
the same moment is a harder problem that already has a real answer
elsewhere in this project (the Yjs/CRDT sync decided for human+agent
live editing) — reuse that mechanism if and when simultaneous same-file
editing is actually needed, don't treat it as required for this ADR.

**Task division reuses what already exists, not a new mechanism.**
When a concurrent unattended session starts, the work gets split via
the existing `create_task` mechanism — either the human assigns
sub-scopes explicitly, or the agents negotiate a split themselves at
the start of the session, using the same tool already built for this,
before diving into independent concurrent work.

**Shared awareness reuses the existing narration pattern.** Both
agents' actions narrate into one shared, durable log — the same
mechanism already built for attended sessions' terminal narration,
extended to interleave two concurrent unattended sessions instead of
one attended one. Each agent's own context includes recent entries from
that shared log, so "aware of what the other is doing" is a real,
concrete mechanism, not an assumption.

## Revisit When

Real usage data exists showing the allowlist is too restrictive for
genuine work, or too permissive given what unattended sessions actually
end up doing — either direction is a real reason to revisit the list
itself, not the underlying principle.

# ADR-010: Local companion IDE — real files, real terminal, presence-gated agent execution

**Status:** Accepted
**Date:** 2026-09-15
**Supersedes:** ADR-009's repo-connection mechanism (GitHub App,
room-scoped Code view, execution deliberately out of scope). ADR-009's
other reasoning — presence-gated safety, git as the real safety net, a
proven CRDT library for live multi-party editing — is carried forward
unchanged, just retargeted at a different file source.

## Context

Mid-build, a materially different and bigger product direction emerged
in a parallel conversation: not a read-only Code view inside a chat
room, but a genuine standalone IDE — a real local project, opened from
the user's own machine, with a real terminal where agents can act
directly, replacing the chat room as the primary surface for that kind
of work. A local "companion" process (WebSocket server, loopback-only,
origin-checked, real shell access) was built toward this before a
formal ADR existed. This document is that ADR, written honestly against
what's real, not retroactively softened to match what was already built.

**This is the single highest-risk piece of software in the entire
project.** Every other agent-initiated action built so far — task
creation, handoffs, search, file attachment — has a bounded, well-
understood blast radius. A real shell does not. Treat every decision
below as load-bearing.

## Decisions

**The IDE is a real, standalone, top-level surface — "Rooms" and
"IDE" as siblings, not one nested inside the other**, matching what
was actually built (the Sidebar's Rooms/IDE switch stays).

**An IDE session is still a room underneath, reusing everything already
built — not a second, parallel system.** Same agents, same BYOK
credential resolution, same cost tracking, same append-only event
history. What's new is that the room's *primary view* is the terminal
instead of the chat timeline, and its agents gain new real tools (file
read/write, shell execution) alongside the ones they already have
(`create_task`, `request_handoff`, `web_search`, `mention_agent`). This
avoids rebuilding orchestration, credential handling, and cost
tracking a second time for no reason.

**The companion is a separately distributed local binary the user
runs and explicitly consents to, once, with real understanding of what
it grants.** Before first use: a real, unmissable consent screen states
plainly that this program executes real commands on the real machine,
on the user's own request or an agent's, and that this is fundamentally
different from anything else in Harmonia. Not a checkbox buried in
Settings — a dedicated, hard-to-miss moment.

**Presence-gated agent execution — the same principle ADR-009 already
established for live editing, applied here as the primary safety
mechanism.** An agent may only write files or execute shell commands
while a human is actively present in that IDE session, watching. The
moment nobody's watching, agent file/shell tools are simply unavailable
— not queued, not approval-gated (an approval click for every shell
command would make the terminal unusable), genuinely absent. This
mirrors why Cursor/Copilot/Claude Code are safe enough to use today:
not an approval gate, a human watching in real time.

**No command blocklist.** Explicitly considered and rejected — blocklists
for a real shell are trivially bypassable and create false confidence
worse than having none. The real safety net is presence plus git,
named honestly with its real limits: a git-tracked file's damage is
recoverable through real history; a destructive command outside a git
repo's scope is not. This limit is stated in the consent screen, not
hidden.

**Every agent-driven action in the terminal is visibly, unambiguously
marked as agent-driven, in real time** — a human watching a session
must never have to guess whether a command was typed by them or
executed by an agent.

**The companion stays loopback-only, origin-allowlisted against
Harmonia's real domains — both already built, both get adversarial
verification, not just the happy path**: confirm a connection attempt
from an arbitrary origin is genuinely rejected, not just that a
matching origin is accepted.

**Tool calls reach the companion only through the browser, by
necessity, not by choice.** A cloud-hosted backend cannot reach a
loopback-bound local process directly — the only real path is backend
→ existing live channel → frontend → the browser's already-open
WebSocket to the companion → result relayed back. This needs to be a
real, defined protocol (message correlation, timeouts, handling a
closed tab mid-command), not ad hoc plumbing.

**Live multi-party collaborative editing still needs a real CRDT
library (Yjs + Monaco), unchanged from ADR-009.** Nothing about the
companion changes that decision — it changes where file content comes
from (a local folder instead of GitHub's API), not how multiple parties
edit it together. This isn't built yet in either direction and remains
real, separately scoped work.

**`migrations/0015_repo_connections` is dead — GitHub-App-mediated repo
connection isn't this direction's mechanism.** Roll it back properly, a
real down migration, not an orphaned unused table sitting in production
schema. A locally opened folder's path is inherently machine-specific
and never belongs in shared backend state the way a BYOK credential
does — if an IDE-session room needs to remember anything about its
local project, that's a display name at most, decided when this gets
built, not a path persisted server-side.

## Revisit When

Multi-user access to the same local project becomes a real need (this
model is fundamentally single-machine, single-user by construction) —
that's a genuinely different architecture, not an extension of this one.

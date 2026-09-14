# ADR-009: IDE foundations — room/code switch, presence-gated live editing, git as the real safety net

**Status:** Accepted
**Date:** 2026-09-14

## Context

This is the first stage of a deliberate, larger pivot: Harmonia becomes
a genuine IDE for human-and-agent collaboration, not just a chat room
that happens to render code blocks. This ADR settles the foundational
trust model the whole direction depends on, before any editor code gets
written — get this wrong and every later stage inherits the mistake.

## Decisions

**One room, two views, not two products.** A room gains a Code view
alongside the existing Chat view, switched via a toggle in the room
header — same room, same agents, same shared context underneath both.
Chat's existing timeline, tasks, handoffs, and approval cards are
untouched; Code is a new lens on the same workspace, not a separate
surface bolted on beside it.

**Live editing is presence-gated, not universally ungated.** When a
human is actively present in a room's Code view, an agent's edits land
live, in real time, the same way a human collaborator's would — no
per-edit approval click. This matches how Cursor, Copilot, and Claude
Code itself already work; the real safety property in all of them isn't
an approval gate, it's a human watching, able to intervene or undo
instantly.

**The moment nobody's watching, that safety property doesn't exist, so
the behavior changes.** If an agent wants to make a code change with no
human actively present in Code view — mid-conversation in Chat, or
simply because nobody has the editor open — it cannot edit live.
Instead it proposes the change via `propose_file_edit`, the same
`agent_action_proposals` mechanism and `ApprovalCard` already proven for
handoffs. Same underlying trust shape, applied to a new action type, not
a new system.

**"Human present" is real, ephemeral presence state, not a proxy.** A
human is "present" in Code view for the purposes of this gate only while
actually connected to it — the same kind of real-time signal already
built for agent status, extended to a genuinely new dimension (human
presence, not agent state). This determines the gate live, per room, not
a static setting.

**Real-time multi-party cursors, visible and labeled**, human and every
active agent, same visual language as Google Docs or VS Code LiveShare —
a room with several participants touching code needs to be legible at a
glance, not a mystery.

**The actual safety net is git, not anything Harmonia builds itself.**
Every room's connected repo does real work on a real branch, with real
commits — independent of whether anything in Harmonia's own UI works
correctly. Harmonia's presence gate and approval fallback are the first
line; git history is the one that holds regardless.

**Repo connection is a GitHub App, not an extension of login OAuth or a
pasted personal access token.** Login OAuth requests minimal scopes on
purpose and shouldn't quietly grow repo-write access. A GitHub App,
installed per-repo with fine-grained permissions and short-lived
installation tokens, is the mechanism GitHub itself recommends for
exactly this shape of access — Harmonia registers the App once, a user
installs it on whichever repos they choose, and Harmonia never holds a
long-lived personal credential for repo access at all. This is a
cleaner trust model than BYOK for this specific case, not a variation of
it.

**Collaborative text sync uses a proven library, not a hand-rolled
engine.** Real-time multi-party text editing is a genuinely hard,
well-studied problem (the same one behind Google Docs, Figma, VS Code
LiveShare) — the responsible choice is a mature CRDT library (Yjs, paired
with Monaco — the same editor component VS Code itself uses — via
existing, maintained bindings) rather than building conflict resolution
from scratch. Same "use the real, proven thing" principle already
applied to native provider search tools.

## Explicitly out of scope for this ADR

Code execution, a terminal, running tests — deliberately unscoped. This
is the exact "never allow arbitrary agent-generated code to execute
directly" line from the original product document, and it doesn't get
designed until the editing foundation above is real and proven, not
before.

## Revisit When

Execution becomes a real, evidenced need once live editing and the
approval fallback are both built and used for real — that's its own
ADR, its own sandboxing design, on the scale of its own infrastructure
project, not an extension of this one.

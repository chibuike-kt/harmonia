# Unattended autonomous sessions — for Claude Code

**Read first:** `docs/adr/ADR-012-unattended-autonomous-sessions.md` in
full, slower than usual. Re-read ADR-010 and ADR-011 before starting —
this is a deliberate, explicit reversal of one specific principle in
ADR-010, not a replacement for either document. Attended sessions must
remain completely unchanged by this work.

## Three batches, hard stop between each — this is the highest-stakes work in the project

## Batch A — the allowlist and network restriction, before anything else touches execution

1. **A real command allowlist for unattended mode**, distinct from
   attended mode's existing (unrestricted, presence-gated) shell
   access. Enumerate explicitly: file operations within the folder,
   real build/test/lint commands, package-manager installs from real
   default registries. Everything else refused, with a clear reason
   given, not a silent failure.

2. **Network restriction**: package-manager installs from real
   registries are the named exception; arbitrary outbound requests are
   not permitted. Write a real adversarial test — attempt a `curl` to
   an arbitrary URL under unattended mode, confirm it's refused.

3. **Folder-scope enforcement, re-verified adversarially for this
   specific mode** — don't assume the existing path-traversal guard
   already covers unattended mode correctly; write a test that tries
   to escape the folder and confirms it's blocked, same rigor as every
   other adversarial test in this project (the origin-check test, the
   self-mention test).

**Stop. Report with real proof: a disallowed command refused, a network
request refused, a path-traversal attempt blocked — each with the
actual refusal reason surfaced, not a bare failure.**

## Batch B — the unattended session itself

4. **A separate, explicit consent step for starting an unattended
   session**, every time, not a one-time toggle — real UI, states
   plainly what's different (nobody will be watching, here's what's
   allowlisted).

5. **Tighter default bounds** in the session-start form for unattended
   mode specifically — suggest more conservative cycle/cost/time
   defaults than attended mode's form, while still requiring the human
   to actively set real numbers, per ADR-011's existing rule.

6. **Full durable transcript** — every action logged completely, not
   just rendered live and discarded. **A real post-session summary**
   surfaced proactively the next time the IDE opens, not something the
   human has to know to look for.

**Stop. Report with real proof: a real unattended session actually
running a real task to completion with nobody watching, the full
transcript existing afterward, and the summary genuinely appearing on
next open.**

## Batch C — concurrent multi-agent collaboration

7. **Two agents working different files in the same folder
   simultaneously** — the required, default shape for this version.
   Same-file simultaneous editing reuses the existing Yjs/CRDT
   mechanism if it comes up; not required here.

8. **Task division via the existing `create_task` mechanism** — no new
   tool. State clearly whether you built explicit human assignment,
   agent-negotiated split, or both.

9. **Shared narration log** — both agents' actions interleave into one
   durable, shared record; each agent's own context includes recent
   entries from it.

**Stop. Report with real proof: two real agents, two real concurrent
unattended sessions, genuinely different files, a real shared log
showing both, and confirm no file-scope or allowlist violation occurred
across either session.**

## Definition of done

An unattended session runs real, bounded, allowlisted work with nobody
watching, refuses anything outside that allowlist with a clear reason,
produces a full record and a real summary afterward. Two agents can
genuinely work the same folder at once on different files, aware of
each other through a real shared log. Every attended-session behavior
from ADR-010/011 remains completely untouched.

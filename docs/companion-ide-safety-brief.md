# Local companion IDE — for Claude Code

**Read first:** `docs/adr/ADR-010-local-companion-ide.md` in full —
this is the highest-stakes ADR in the project so far, read it slower
than usual. Treat the existing companion/IDE code as a real head start,
not something to rebuild — but nothing in it ships until this brief's
gaps are closed.

## First: restart what got left down

Postgres was stopped mid-session and never restarted. Confirm it's
running before anything else, and confirm `docker compose run --rm
migrate up` applies `0016_drop_repo_connections` cleanly against the
current dev database (which has `0015` applied with real rows in it, so
the down direction is a genuine rollback, not a no-op).

## Batch 1 — the gaps that make this safe to keep building on

1. **The consent screen.** Before the companion's WebSocket ever
   accepts a connection for the first time on a given machine, the user
   must see a real, dedicated, hard-to-miss screen stating plainly what
   they're enabling — real command execution, on their real machine, by
   them or by an agent — and explicitly consent. Not a checkbox in
   Settings, not a line in a README.

2. **Presence-gated agent execution.** Agent file-write and shell-exec
   tools must only be available to a generation call when a human is
   genuinely, currently present in that IDE session — same mechanism
   already built for the room-based live-editing presence gate in
   ADR-009's design (even though that specific feature isn't built yet,
   the presence-signal infrastructure it implies is the right thing to
   share here, not reinvent). When nobody's present, those tools are
   absent from what the agent is offered — not queued, not gated behind
   approval, genuinely not there.

3. **Real-time agent-action visibility in the terminal.** Every command
   an agent executes must be visibly, unambiguously marked as
   agent-driven the instant it happens — a human watching must never
   have to guess whether something was typed by them.

4. **Adversarial origin-check verification.** Write a real test
   confirming a WebSocket connection attempt from an origin *not* on
   the allow-list is genuinely rejected — not just that the real
   Harmonia origin is accepted. This is the one property standing
   between "a local tool only Harmonia can reach" and "any webpage can
   reach a local shell," so it needs proof, not confidence.

5. **The relay protocol, formalized.** Backend → existing live channel
   → frontend → the browser's WebSocket to the companion → result
   relayed back — this needs real message correlation and timeouts, and
   a defined, tested behavior for what happens if the browser tab
   closes mid-command (the agent's tool call should fail cleanly, not
   hang the way the provider-timeout bug did a few rounds back — same
   lesson, applied here before it becomes the same kind of silent-hang
   incident).

**Stop. Report on all five with real proof — the consent screen
actually blocking first use, the presence gate actually withholding
tools when nobody's watching, the adversarial origin test actually
failing the way it's supposed to before the fix and passing after.**

## Batch 2 — reconciling with what already exists

6. Confirm whether an IDE session maps to a real room in the existing
   schema (per the ADR's recommendation) or needs a lighter, separate
   concept — implement per the ADR's reasoning, flag clearly if
   something about the existing room model doesn't fit cleanly once you
   look closely.

7. Wire the companion's new tools (file read/write, shell exec) into
   the existing tool-declaration mechanism (`provider.ToolDef`) — same
   infrastructure `create_task`/`request_handoff`/`mention_agent`/
   `web_search` already use, not a second tool system.

## Explicitly not in this brief

Live multi-party collaborative editing (Yjs/Monaco CRDT sync) — real,
separately scoped work per the ADR, not part of closing these gaps.

## Definition of done

A fresh install of the companion requires real, explicit consent before
it will do anything. An agent cannot write a file or run a command
unless a human is genuinely watching that session right now. Every
agent action in the terminal is visibly marked as the agent's, not the
human's. A connection attempt from an unauthorized origin is proven,
not assumed, to fail. The dead migration is rolled back cleanly.

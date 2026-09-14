# IDE Stage 1 — repo connection and file tree — for Claude Code

**Read first:** `docs/adr/ADR-009-ide-foundations.md` in full — it
settles decisions that shape this stage even though live editing itself
isn't built yet. `migrations/0015_repo_connections.up/down.sql`
(already written, confirm it applies cleanly).

## Why this stage is scoped this narrowly

The ADR covers the whole foundation — presence-gated live editing, the
approval fallback, git as the real safety net. **None of the live-editing
infrastructure (Yjs, Monaco, real-time cursors) gets built in this
stage.** This project has proven, repeatedly, that the smallest real
version of a big idea should land and be verified before the riskier
next piece — chat proved 1:1 before multi-agent, tool use proved
cascading before autonomous pickup. This stage proves "connect a real
repo, agents can genuinely read real files" before anything writes to
one.

## Build order

1. **GitHub App registration** — this is Kingsley's task, not yours:
   registering a real GitHub App (not expanding login OAuth's scope, not
   a personal access token field) needs a human decision about
   permissions and a real App identity on GitHub's side. Build the
   installation flow assuming the App already exists and its
   credentials are in `.env` — don't attempt to register it
   programmatically.

2. **Backend: installation flow.** A room owner connects a repo via
   GitHub's real App-installation flow (not a pasted token) — the
   callback records a `repo_connections` row (owner, repo, default
   branch, installation ID). Installation tokens are fetched on demand
   from the installation ID and the App's own private key, short-lived,
   never persisted — same "never store what you can resolve fresh"
   discipline already applied to BYOK credentials, just with GitHub's
   own token exchange instead of Harmonia's encryption.

3. **Backend: read-only file access.** Given a room's `repo_connections`
   row, fetch real file content from the connected repo/branch via
   GitHub's API using the installation token. No writes anywhere in this
   stage.

4. **Frontend: the Chat/Code toggle**, per the ADR — a header control
   switching the room's view. Code view, for this stage, is a real file
   tree (GitHub's actual repo structure) plus a read-only file viewer —
   not an editor yet. No live-editing chrome, no presence indicators,
   nothing this stage doesn't actually back with real behavior.

5. **Agent context: real files, not descriptions of files.** Extend the
   existing context-assembly path (the same one already carrying
   attachments and room framing) so an agent invoked in a room with a
   connected repo can be given real file content as part of its context
   — reuse the attachment-content plumbing's shape rather than inventing
   a second way to hand a model file content.

## Definition of done

A room owner connects a real repo through a real GitHub App install, no
pasted token anywhere. A human can browse the real file tree and read
real file content in Code view. An agent, when relevant, can reference
real file content in a reply — grounded, not invented. Nothing writes to
the repo. Live proof required, same standard as every other stage: a
real connected repo, a real file read by a human and separately by an
agent, both genuinely reflecting the file's real content.

## What's next, not now

Stage 2 — the actual live editor (Yjs + Monaco), the presence gate, the
`propose_file_edit` approval fallback, real commits — is its own brief,
written once this stage is reviewed and proven. Don't start on it here.

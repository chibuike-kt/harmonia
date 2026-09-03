# Phase 2 Build Brief — Auth, Sessions, BYOK Credentials — for Claude Code

**Read first, in this order:** `CLAUDE.md`, `CONTRIBUTING.md`,
`docs/adr/ADR-001-language-decision.md`,
`docs/adr/ADR-002-user-auth-and-byok-credentials.md`,
`docs/milestone-1-design.md`. ADR-002 is the source of truth for every
decision in this brief — this document is build order, not re-justification.

## Objective

Close both debt-marker comments left in Milestone 1 (`POST /v1/rooms` and
`POST /v1/rooms/{room_id}/agents` are currently open with no human auth),
and make BYOK real: providers stop coming from platform env vars and start
coming from each user's own encrypted, verified credential.

## What already exists — do not redesign it

- `migrations/0002_users_sessions_credentials.up/down.sql` — schema for
  this phase, already written and reasoned about in ADR-002. Apply it,
  don't alter its shape without flagging why first.
- `internal/agent/auth.go` — the API-key auth pattern this phase's session
  auth structurally mirrors. Read it before writing `user.Authenticate`;
  don't reinvent the shape, and don't let the two systems share code,
  middleware, or context keys. They authenticate different kinds of
  caller and must stay genuinely separate.
- The AES-256-GCM encryption pattern already used for other credential
  storage in this codebase's history (Keel's wallet work) — same
  primitives, same discipline: encrypt at rest, decrypt only in memory at
  call time, never log plaintext.
- `internal/room`, `internal/agent`'s existing handlers — this phase adds
  auth requirements to routes that already exist, it doesn't replace them.

## What's explicitly out of scope — do not build any of this now

Multi-provider account linking (one user, both GitHub and Google), MFA,
password auth of any kind, org/workspace-level shared credentials, rate
limiting on auth endpoints (real gap, worth its own pass later, not this
one), the chat/`messages` table (separate future phase), billing/plan
enforcement. If a task seems to need one of these, stop and flag it.

## Prerequisite — not yours to do

Kingsley needs to register an OAuth App with GitHub and a project with
Google (OAuth consent screen + credentials), and put the resulting client
ID/secret pairs plus callback URLs into `.env`. You can build and test
everything below against fake OAuth providers without these existing —
see step 9 — but live verification against the real providers can't
happen until they're in place. Don't block on this; flag it as the
remaining gap in your final report, the same way the Anthropic key was
handled in Milestone 1.

## Build order

One PR per step, same discipline as Milestone 1.

1. **`chore(migrations): apply 0002_users_sessions_credentials`**
   Confirm it applies cleanly against a fresh and against an existing
   Milestone-1-seeded database (nullable `rooms.owner_id` means existing
   rooms shouldn't break). Write the domain package `internal/user` with
   a `Store` following the existing `room`/`agent` `Store` pattern.

2. **`feat(auth): GitHub OAuth login and callback`**
   `GET /v1/auth/github/login` redirects to GitHub's authorize URL with a
   CSRF `state` parameter (store it server-side or in a short-lived
   signed cookie — your call, document which). `GET /v1/auth/github/callback`
   exchanges the code, fetches the GitHub user, upserts into `users` by
   `github_id`, issues a session (see step 4), sets the cookie, redirects
   into the app.

3. **`feat(auth): Google OAuth login and callback`**
   Same shape as step 2, OIDC-flavored — verify the ID token, upsert by
   `google_id`.

4. **`feat(auth): session issuance, user.Authenticate middleware, FromContext`**
   Mirrors `internal/agent/auth.go`'s shape exactly, in its own package
   (`internal/session` or `internal/user`, your call — keep it separate
   from `internal/agent`). Session token generation, hashing, cookie
   issuance, the middleware that resolves a request's session to a user
   and 401s on missing/expired/revoked, and `FromContext` for downstream
   handlers. This is what steps 2 and 3 both call to actually issue a
   session once a user is resolved.

5. **`feat(auth): active sessions — list, revoke, logout`**
   `GET /v1/sessions` — the authenticated user's own sessions, with a
   flag on whichever one matches the current request's cookie.
   `DELETE /v1/sessions/{id}` — revoke one (set `revoked_at`, don't
   delete the row — a revoked session is still a real historical record).
   `POST /v1/auth/logout` — revoke the current session and clear the
   cookie.

6. **`feat(credentials): BYOK provider credential store`**
   `POST /v1/credentials` — body `{provider, key}`. Runs a cheap live
   verification call against that provider before storing anything (reuse
   `internal/provider`'s `Agent.Generate` with a minimal prompt, or a
   lighter provider-specific check if one exists — your call, note which).
   Encrypts with AES-256-GCM, stores `encrypted_key`/`nonce`/`key_hint`
   (last 4 chars of plaintext). `GET /v1/credentials` — list the
   authenticated user's connected providers, hints only, never the
   encrypted blob. `DELETE /v1/credentials/{provider}` — remove one.
   All three require `user.Authenticate`.

7. **`feat(room): require session auth, set owner_id`**
   `POST /v1/rooms` now requires `user.Authenticate`; sets `owner_id` from
   the session. This is the fix for the debt-marker comment in
   `internal/room/http.go` — remove the comment along with the fix, don't
   leave it as a stale note once it's resolved.

8. **`feat(agent): require session auth + room-ownership check on registration`**
   `POST /v1/rooms/{room_id}/agents` now requires `user.Authenticate` and
   checks the session's user matches the room's `owner_id` — 403 on
   mismatch, 404 if the room doesn't exist (same non-leaking pattern as
   the rest of the API). Fixes the second debt-marker comment.

9. **`feat(provider): resolve credentials from BYOK, not env vars`**
   Wherever `anthropic.New(...)`/`openai.New(...)` are currently
   constructed from `ANTHROPIC_API_KEY`/`OPENAI_API_KEY`, change to look
   up `provider_credentials` for `(room.owner_id, agent.provider)`,
   decrypt in memory, construct the client per-call. Keep the env vars as
   an explicit dev-only fallback (clearly logged/commented as such, not
   silently preferred) — Milestone 1's acceptance test still needs to run
   without a full OAuth+BYOK setup in CI.

10. **`feat(user): minimal settings — GET/PATCH /v1/users/me`**
    Username and display name only, for now. Both require
    `user.Authenticate`. This is intentionally small — "settings" in this
    phase is really just this plus the sessions and credentials endpoints
    from steps 5/6, not a new subsystem.

11. **`test(auth): fake OAuth provider for automated tests`**
    Real GitHub/Google can't run in CI. Build a minimal `httptest.Server`
    standing in for each provider's token + userinfo endpoints (same
    technique already used for the Anthropic client's structural test in
    Milestone 1), point the OAuth client at it via a configurable base
    URL, and write integration tests for the full login → session →
    authenticated-request flow through it. State plainly in your report
    that this proves the OAuth *code path* works, not that the real
    GitHub/Google integration works — that needs the real client
    ID/secret from the prerequisite above, and stays unverified until
    then, the same way the Anthropic client sat structurally-verified-
    only until a real key existed.

## Constraints that apply to every step above

- Follow `CLAUDE.md` and `CONTRIBUTING.md` — atomic commits, no AI
  attribution, stage and stop.
- Session cookies are `HttpOnly`, `Secure`, `SameSite=Lax`, always — no
  exceptions for local-dev convenience that could leak into production
  behavior.
- The plaintext BYOK key is never logged, never returned by any endpoint
  after the initial `POST /v1/credentials` response (and even that
  response shouldn't include it — verify-then-store, respond with the
  hint only).
- `agent.Authenticate` and `user.Authenticate` stay genuinely separate
  systems. A route needs one or the other, and it should be obvious from
  reading the route which one, not something you have to trace through
  middleware to figure out.
- Every new endpoint gets a test before it's done, per `CONTRIBUTING.md`.

## Definition of done

Migration applies cleanly. A user can be created via the fake-OAuth test
flow, gets a session, can create a room (now owned), register an agent
into it (now ownership-checked), connect a BYOK credential (verified,
encrypted at rest), and have that agent's `Generate` calls actually use
the stored credential instead of the platform env var. Active sessions
can be listed and revoked. `go vet ./...` and `go test ./...` clean, CI
green. Real GitHub/Google OAuth and real BYOK verification against live
providers remain explicitly flagged as pending the OAuth app registration
prerequisite — not silently treated as done.

# ADR-002: User auth via OAuth + opaque sessions, BYOK credentials encrypted at rest

**Status:** Accepted
**Date:** 2026-09-04

## Context

Milestone 1 has no human accounts — only agent-to-platform machine auth
(scoped API keys, `internal/agent/auth.go`). Several routes were left
intentionally open with debt-marker comments (`POST /v1/rooms`,
`POST /v1/rooms/{room_id}/agents`) pending this phase. Separately, the
product discussion settled on BYOK: users connect their own Anthropic/
OpenAI credentials rather than the platform holding one shared key.

## Decisions

**Identity: OAuth only, GitHub and Google.** No password auth, ever — this
audience already has both, and password storage is a liability with no
offsetting benefit here. A user is created on first successful OAuth
callback from either provider; `users.github_id`/`users.google_id` are
both nullable but at least one is required (see migration's CHECK
constraint). Linking both providers to one account is out of scope for
this phase — flagged as a real future feature, not forgotten.

**Sessions: opaque tokens, not JWT.** A session token is 32 random bytes
(same `crypto/rand` pattern as agent keys), returned once as an
`HttpOnly`/`Secure`/`SameSite=Lax` cookie, stored server-side as a SHA-256
hash in `sessions`. This is a deliberate structural parallel to
`internal/agent/auth.go` — same shape, same reasoning, but a genuinely
separate system. **These two auth layers must never be conflated**: agent
API keys authenticate a machine acting in one room; sessions authenticate
a human across the whole product. A handler needs at most one of the two,
never both, and `user.Authenticate` and `agent.Authenticate` should not
share middleware or context keys.

The alternative (JWT) was rejected specifically because "active sessions,
revocable individually" is a hard requirement here, and a JWT only gets
you that by also building a server-side revocation list — at which point
you've built this table anyway, with more moving parts.

**BYOK credentials: encrypted at rest, decrypted only in memory at call
time.** AES-256-GCM, matching the encryption pattern already proven in
production on the custodial wallet work. Each user gets at most one
credential per provider (`UNIQUE(user_id, provider)`); reconnecting
replaces it. A credential is verified with a cheap live call before it's
stored — a bad key is caught at connection time, not three tasks into a
room. Only a `key_hint` (last four characters) is ever returned by the
API; the encrypted blob and the decryption key never leave the server
process, and the plaintext key is never logged.

**Room ownership.** `rooms.owner_id` references `users.id`, nullable (
existing Milestone 1 rooms have no owner and that's fine — they were
created before humans existed in this system). Going forward, room
creation requires a session and sets `owner_id` from it. Agent
registration into a room requires the caller's session to match that
room's `owner_id` — this is the fix for both debt-marker comments left in
`internal/room/http.go` and `internal/agent/http.go`.

**Provider credential resolution.** When an agent needs to call out to its
provider, the platform looks up `provider_credentials` for
`(room.owner_id, agent.provider)`, decrypts in memory, and uses that key
for the call. The `ANTHROPIC_API_KEY`/`OPENAI_API_KEY` env vars used for
Milestone 1's acceptance test become a dev-only fallback, clearly marked
as such — not the real credential path going forward.

## Revisit When

Multi-provider account linking, org/workspace-level credentials (as
opposed to per-user), or a move away from cookie sessions toward
API-token-based programmatic access for the eventual SDK (section 57 of
the original product overview) would each be a real reason to revisit
this ADR — not before.

`credentials.Store.Resolve` (BYOK credential lookup, decrypt, and client
construction for `(room.owner_id, agent.provider)`) is built, tested, and
correct as of this phase, but has no production caller yet — nothing in
the codebase decides to invoke a provider on an agent's behalf today.
That decision belongs to the orchestration engine, which doesn't exist
until a later phase (Phase 4's tool execution). Wiring `Resolve` into a
real call site, once that engine exists, is expected — not itself a
reason to revisit this ADR's decisions.

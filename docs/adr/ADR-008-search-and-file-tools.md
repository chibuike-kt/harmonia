# ADR-008: Web search and file attachment

**Status:** Accepted
**Date:** 2026-09-08

## Context

The ADR-004 addendum on native provider tools flagged search and simple
file attachment as small, near-term work once chat was proven — chat
has since been proven repeatedly (multi-mention, cascading,
agent-initiated actions, autonomous pickup). This is that work, and it
turns out search and files aren't the same shape of feature at all.

## Decisions

**Search has two independent trigger paths, not one.**

- **Standing, room-level toggle** (`rooms.web_search_enabled`, default
  `false`) — same placement and pattern as cascading and autonomous
  pickup. When on, every generation call for agents in that room
  declares the web search tool with normal, advisory `tool_choice` — the
  model decides per-turn whether a given reply actually needs it, the
  same way Anthropic's and OpenAI's own search tools are designed to
  work.
- **Per-message explicit attach** — the composer's existing "Search the
  web" placeholder (honestly labeled "soon" since it was first built)
  becomes real. Attaching it to one message **forces** the tool call for
  that specific generation, via `RequireToolCall`, regardless of the
  room toggle's state.

**This is a legitimate second production use for `RequireToolCall`, not
scope creep on a test-only mechanism.** Its scope was narrowed once
already to exclude real reply generation, specifically because forcing
a tool call onto an answer the human never asked to be structured that
way is a real distortion. A human explicitly clicking "search this" *is*
that missing signal — the exception isn't loosening the rule, it's
recognizing the rule's actual condition (no explicit human intent) isn't
met here.

**OpenAI needs an additive second path, not a migration.** Its search
tool only exists on the Responses API; the existing client uses Chat
Completions, proven everywhere else in this build. A second code path
engages only when search is actually needed for a given call (room
toggle on, or per-message forced) — Chat Completions stays untouched for
every other call. Anthropic's client needs less structural change, same
Messages API endpoint already in use, just a conditionally-declared tool.

**Files are the opposite of search: per-message only, no standing
toggle** — "always attach a file" isn't a coherent capability the way
"always allow search" is. Because invocation is asynchronous (a
background job, not the request itself), an attached file's content has
to genuinely survive past the initial HTTP call. Stored directly on the
message row (new nullable columns), not real object storage — files are
expected to be small (a snippet, a screenshot), and standing up the
fuller object-storage system the original product doc described stays
correctly deferred. Size-capped both at the application layer and via a
database `CHECK` constraint — defense in depth, same reasoning as every
other DB-level guardrail in this project.

**Citations get a real, if simple, UI treatment.** A search-grounded
reply renders numbered inline markers with a compact source list at the
end, built from each provider's own citation metadata — not a new
invention, just rendering what the API already returns.

**Cost stays fully transparent for tokens, with one known, accepted gap.**
Both search paths and any file-related tokens flow through the existing
token/cost-tracking mechanism — same cost pill, same principle that's
driven every prior cost-adjacent decision in this build. That covers
tool-use overhead on both providers and the Responses API's differently-
named-but-equivalent usage fields (see `provider.GenerateResponse`'s own
doc comment). It does **not** cover Anthropic's separate flat per-search
fee ($10 per 1,000 searches, reported as
`usage.server_tool_use.web_search_requests` — a real count, not folded
into token usage at all). OpenAI has no equivalent line item — its
search cost is already inside ordinary token usage — so this gap is
Anthropic-specific, not a symmetry problem between the two providers.

**Decision: document the gap, don't estimate it yet.** The existing cost
pill (`apps/web/src/lib/tokenPricing.ts`) already carries a hardcoded,
explicitly "NOT AUTHORITATIVE" per-model price table in the same spirit
a rough search-fee estimate would need — that precedent alone would make
adding one a small change. It isn't one in practice: nothing in this
codebase currently captures a per-message search count at all, so a
real estimate needs a new `GenerateResponse` field, live-verified
parsing of Anthropic's `web_search_requests` count (not possible to
verify in this build's own environment, which has no live
`ANTHROPIC_API_KEY`), a new persisted column on the message row (a real
migration), and threading it onto the wire and into the pill — a small
vertical slice through four layers, not a table edit, for a provider
this build can't currently test live. Revisit this once Anthropic has a
live key available to verify the parsing against, or if a real user
reports the pill under-representing search-heavy room spend.

## Revisit When

Real object storage becomes necessary for a reason beyond this feature
(large files, files that need to persist independent of one message) —
that's the original product doc's fuller Artifact/object-storage system,
a different and larger decision than this one.

# Web search and file attachment — for Claude Code

**Read first:** `docs/adr/ADR-008-search-and-file-tools.md`,
`migrations/0014_search_and_files.up/down.sql` (already written, confirm
it applies cleanly). Also re-read the doc comment history on
`RequireToolCall` — this brief extends its scope a second time, with
real reasoning, not casually.

## Two largely independent batches — files first, it's smaller and has no OpenAI-client wrinkle

## Batch A — file attachment

1. **Backend:** `POST /v1/rooms/{room_id}/messages` accepts an optional
   attachment (content, filename, mime type), stored directly on the
   message row per the migration. Enforce the size cap at the
   application layer too, before ever attempting the insert — the DB
   constraint is the backstop, not the only check, same defense-in-depth
   reasoning as everywhere else in this project.

2. **Passing the attachment to `Generate`:** extend
   `provider.GenerateRequest` to carry it through to each provider's
   real inline content format (Anthropic's document/image content
   blocks, OpenAI's equivalent) — reuse the existing recency-window
   context assembly, don't build a second context path for messages that
   happen to have an attachment.

3. **Frontend:** the composer's "Add a file" placeholder becomes real —
   file picker or drag-and-drop, a pending-attachment chip (reuse the
   existing `FileCard` component from the pasted-text work), sends
   alongside the message.

**Stop. Report with real proof: attach a real small file, send it,
confirm the agent's reply genuinely references the file's actual
content — not just that the request didn't error.**

## Batch B — web search

4. **`rooms.web_search_enabled`** — new toggle in Room Info, same
   placement and pattern as cascading/pickup, off by default.

5. **Anthropic:** declare the web search tool (advisory `tool_choice`)
   on every generation call when the room toggle is on. Same Messages
   API endpoint already in use — confirm whether any other structural
   change is needed beyond the conditional tool declaration, and state
   plainly if there isn't one.

6. **OpenAI — the real structural piece.** Search only exists on the
   Responses API. Build a second, additive code path in the OpenAI
   client that's used only when search is actually needed for a given
   call (room toggle on, or the per-message forced case from step 7) —
   the existing Chat Completions path must stay completely untouched for
   every other call. Don't let this become a wholesale migration; that's
   explicitly not what this ADR calls for.

7. **Composer's "Search the web" placeholder becomes real, per-message,
   forced.** Attaching it to one message declares the search tool with
   `RequireToolCall` for that specific generation — regardless of the
   room toggle's state. Update `RequireToolCall`'s doc comment to record
   this as its second legitimate production use, with the actual
   reasoning (explicit human intent is the missing signal the original
   restriction was protecting against) — not just "also used here now."

8. **Citations.** Render each provider's citation metadata as numbered
   inline markers plus a compact source list at the end of a
   search-grounded reply. Don't invent citation data — if a provider's
   response doesn't include it for a given search, the reply just has no
   citations, don't fabricate a plausible-looking source list.

9. **Cost.** Confirm search-related tokens (both the tool-use overhead
   and the OpenAI Responses-API path specifically, which may have a
   different token accounting shape than Chat Completions) are captured
   correctly in the existing cost tracking. State plainly if you find
   the two paths' usage fields aren't symmetrical — don't silently
   assume they match Chat Completions' shape.

**Stop. Report with real proof: a room with search enabled, a real
question that genuinely benefits from search, a real reply with real
citations. Separately, a room with search off, the same forced
per-message attach still producing a real, cited, search-grounded
reply — proving the two trigger paths are genuinely independent.**

## Constraints across both batches

- Never let file or search handling touch the recency-window context
  assembly's existing behavior for ordinary messages without either.
- The size cap and the `RequireToolCall` scope both need their reasoning
  written down in code comments, not just in this brief — same standard
  as every other non-obvious decision in this project.

## Definition of done

A file attached to a message genuinely informs the agent's reply. A
room with search enabled produces real, cited answers when a question
warrants it, and doesn't force search on irrelevant chit-chat. The
per-message forced-search override works independently of the room
toggle. Real cost is visible and accurate for every new path. OpenAI's
existing Chat Completions behavior is provably unchanged for calls that
don't need search.

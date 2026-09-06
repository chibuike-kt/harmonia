# ADR-005: Settings as a modal — real categories, honest placeholders

**Status:** Accepted
**Date:** 2026-09-06

## Context

`/connect-agents` exists as a standalone page. Sessions
(`GET`/`DELETE /v1/sessions`) exist only as API endpoints — no UI has
ever been built for them; the settings mockup is their first design.
The sidebar's profile menu has had an inert "Settings" item since Phase
3. This ADR makes it real.

## Decisions

**Modal, not a page.** Settings are account-level configuration, not
something needing a bookmarkable, deep-linkable URL the way a room
does. A modal keeps the persistent shell (sidebar, current room)
present underneath rather than navigating away from it.

**Three real categories, four honest placeholders.** General (profile),
Connected agents, and Sessions are fully functional against real
endpoints. Security, Team & roles, Billing, and Notifications render
plain, truthful explanations of why they're not built yet — same
"honestly labeled, not fake-functional" standard as the composer's `+`
menu.

**`/connect-agents` is retired as a standalone page, redirected into
the modal.** Maintaining two separate UIs for the same BYOK credential
feature is a real, avoidable inconsistency risk — one becomes stale
while the other gets fixes. The modal's "Connected agents" category
becomes the only interface.

**Two new fields on `users`, both nullable:**
- `preferred_name` — distinct from `username`/`display_name`. What the
  app and agents call someone casually, not their account identity.
  Feeds the dashboard's rotating greeting, which has been hardcoded to
  a literal name since it was built — this is the real fix for that.
- `custom_instructions` — free text, included in every agent `Generate`
  call's context, prepended the same way the recency-window message
  history already gets assembled. A small addition to existing context-
  building logic, not a new system.

## Revisit When

Team & roles or Security need to become real — that's a multi-tenancy
phase on the scale of the original auth/BYOK work, not a settings
sub-page.

# Settings modal — for Claude Code

**Read first:** `docs/adr/ADR-005-settings-modal.md`,
`docs/design/settings-mockup.html` (the visual/interaction target).

## Backend

1. Migration: add `preferred_name` and `custom_instructions` (both
   nullable) to `users`.
2. Extend `PATCH /v1/users/me` to accept both new fields, same
   COALESCE-omitted-fields-unchanged pattern already used for
   `name`/`pinned` on rooms.
3. Wire `custom_instructions` into the existing reply/title generation
   context assembly — prepend it when present, same place the recency-
   window messages already get assembled for `Generate`.

## Frontend

4. Build the modal per the mockup: search box, category list on the
   left, content on the right. Wire the sidebar profile menu's
   "Settings" item (currently inert) to actually open it.
5. **General** — profile form (existing `PATCH /v1/users/me` fields)
   plus the two new ones: "What should agents call you?"
   (`preferred_name`) and "Instructions for your agents"
   (`custom_instructions`).
6. **Connected agents** — port the existing `/connect-agents`
   functionality into this category. Redirect the standalone route here
   instead of maintaining both.
7. **Sessions** — first real UI for `GET`/`DELETE /v1/sessions`: list,
   "this device" tag, revoke. Reuse the logout-on-401 safety logic
   already built for route protection if revoking the current session
   is possible from here.
8. The four placeholder categories (Security, Team & roles, Billing,
   Notifications) render the honest static explanations from the
   mockup — no backend calls, nothing clickable inside them.
9. Update the dashboard's rotating greeting to use the real
   `preferred_name` (falling back to existing behavior when unset)
   instead of whatever placeholder name it currently has.

## Definition of done

Settings opens from the profile menu as a modal, not a navigation.
General/Connected agents/Sessions are fully real. `/connect-agents`
redirects into the modal. The four placeholders are honest, not
functional. The dashboard greeting uses a real name. Backend and
frontend verification at the same standard as every prior phase —
live click-through, not just clean builds.

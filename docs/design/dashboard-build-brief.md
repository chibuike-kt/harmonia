# Dashboard shell + launcher — for Claude Code

**Reference file:** `docs/design/dashboard-mockup.html`, committed alongside
this brief. Exact visual/interaction target for the sidebar and the main
launcher view.

## Objective

This is bigger than one page. Right now there's no persistent app shell at
all — `/rooms/new` and `/rooms/[id]` are flat, standalone routes with
nothing wrapping them. This brief builds the actual shell (sidebar +
outlet) that Claude/ChatGPT-style apps have, and the dashboard/launcher
screen that lives in it by default.

## Real backend work this surfaces — not optional, not a detour

The mockup's room list needs data no endpoint currently provides.

1. **`GET /v1/rooms`** — list the authenticated user's own rooms
   (`owner_id` = session user), each with: id, name, a timestamp for
   recency sorting, and whether the room currently has any agent with
   `status = 'running'` (an `EXISTS` subquery against `agents` per room is
   cheap; don't do N+1 queries).

2. **Recency needs a real signal, not room-creation order.** The honest
   answer is a `rooms.last_activity_at` column, updated in the same
   transaction as every `event.Store.Record` call already makes (same
   transactional pattern already established for task/handoff writes —
   this is one more write inside an existing transaction boundary, not a
   new one). That's a migration (`00XX_rooms_last_activity.up/down.sql`)
   plus updating the existing transactional handlers. Don't fall back to
   `rooms.created_at` as a silent shortcut — if you think the migration
   is out of scope for this task, say so and ask, don't quietly
   approximate recency with the wrong field.

3. **Requires `user.Authenticate`** and the same non-leaking room-ownership
   pattern already used everywhere else in this API.

## Frontend work

1. **Persistent authenticated layout** — sidebar + main outlet, applied
   to at minimum: the new dashboard route and the existing `/rooms/[id]`
   view, so navigating from the dashboard into a room keeps the sidebar
   in place instead of losing it on a full page transition. Route
   structure (Next.js route groups, a shared layout, whatever fits this
   codebase) is your call — the requirement is behavior, not mechanism.

2. **Sidebar** — port the mockup's three behaviors faithfully, all pure
   frontend, no backend dependency:
   - Collapse to genuinely zero width (not an icon rail) via the
     top-bar arrow; a small peek button remains, hover floats the full
     sidebar back over main as an overlay (main doesn't reflow), click
     makes it permanent.
   - Drag-to-resize via the edge handle, independent of collapse.
   - The rooms-section chevron (right side, next to sort, appears on
     sidebar hover) collapses just the room list.

3. **Quick-nav — real destinations only, no invented pages:**
   - Dashboard → this new page
   - New room → existing `/rooms/new`
   - Agents → existing `/connect-agents` (real page, real destination —
     don't build a new one)
   - Activity → **no backend or page exists for this.** Leave it as an
     inert link, exactly like the mockup's `#` placeholders. Do not build
     a new page or endpoint for it as part of this task.

4. **Profile menu** — Search, Team & roles, Security, Settings, Get help
   all stay inert placeholders; none of those concepts (org/teams,
   security settings, a settings page) exist in the backend yet. **Log
   out is real** — wire it to the existing `POST /v1/auth/logout`.

5. **Rotating heading** — port the mockup's logic (time-aware greeting +
   rotating prompts, never repeating back-to-back), but pull the real
   name from `GET /v1/users/me` instead of a hardcoded string.

6. **Room list rendering** — consume the new `GET /v1/rooms`, render
   name + relative time (`now`/`2h`/`1d`, compute client-side from the
   real timestamp) + the live dot when the endpoint reports an agent
   running. If the list is empty, this needs its own honest empty state
   — the mockup only shows the populated case; don't ship a blank sidebar
   with no explanation for a brand-new user with zero rooms.

7. **The orb** — reuse exactly what's already built for the login page's
   Nav component (`ThinkingOrb`, real monochrome behavior). Don't
   reintroduce the mockup's teal canvas approximation; that question was
   already settled during the login page build.

## Explicitly out of scope for this task

An Activity page or endpoint, a Settings page, Team/roles or Security
pages, retrofitting `/connect-agents` to live inside the new persistent
shell (it stays a standalone page for now), any real search
functionality behind the "Search" menu item. All real, all deferred —
not forgotten, not silently resolved by inventing something.

## Definition of done

A logged-in user lands on the dashboard, sees their real rooms
(recency-sorted by real last-activity, live-status accurate), can
collapse/peek/resize the sidebar exactly per the mockup, clicking a room
takes them into it without losing the sidebar, and Log out actually logs
them out. Report plainly which parts required backend changes beyond the
brief's own list (there may be gaps I didn't anticipate) versus what was
a direct frontend port.

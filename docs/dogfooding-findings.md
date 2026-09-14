# Dogfooding findings — full end-to-end pass

**Date:** 2026-09-13/14
**Scope:** Signed-in user (existing account, real GitHub OAuth), real OpenAI
BYOK credential, real gpt-4o-mini agents, real Postgres/Redis behind the
running dev stack. No Anthropic credential was available, so all scenarios
ran on OpenAI agents only. This was a live, hands-on pass through the
product as a real engineer would use it — not a scripted test suite run —
including deliberate attempts to break things. Every claim below was
independently verified: either observed directly in the browser, or
confirmed against the database/API where the UI alone wasn't conclusive
proof.

**Note on scope:** two setup steps in the original plan were not performed
directly — creating a brand-new account via GitHub OAuth, and typing a
real OpenAI API key into the Settings BYOK field — both fall outside what
an agent should ever do on a user's behalf. The user connected the BYOK
credential themselves ahead of time; the existing, already-populated
account was used in place of a from-scratch signup.

---

## What genuinely felt smooth

- **GitHub login, when already authenticated in-browser, was a single
  click with no visible delay** — straight from the marketing-page login
  card to the dashboard. No form, no second confirmation screen.
- **Room creation asks for nothing.** "Create a new room" drops you
  straight into an empty room named "New room," cursor-ready to type —
  no name field, no setup wizard.
- **Adding a second same-provider agent auto-distinguished itself
  correctly and instantly** — "ChatGPT" then "ChatGPT 2," reflected
  immediately in the agent pill row and the composer's quick-address
  chips.
- **Autonomous pickup was the single cleanest moment of the whole pass.**
  A genuinely unaddressed message describing a real ops problem, in a
  two-agent room, got picked up by exactly one agent within ~8 seconds,
  produced a real, well-formed task, and the room auto-titled itself
  correctly — no duplicate response from the second agent, no wasted
  spend, no nudging required.
- **Both web-search paths worked correctly on the first try**, no
  prompting tricks needed. The room-level toggle produced a real,
  cited, search-grounded answer to a real question; the per-message
  forced attach did the same independently, with the room toggle
  confirmed off in the database at the time. Citations rendered as real
  clickable numbered links with a proper "Sources" list.
- **A real attached file was genuinely read and reasoned about**, not
  just accepted — the reply correctly quoted the specific field and
  value from the uploaded Go file and gave sensible advice based on it.
- **The 3-hop cascade depth cap fired exactly as documented**, with a
  plain, human-readable message ("this room's automatic agent-to-agent
  chain reached its 3-hop limit here and stopped") rather than silently
  truncating. This is the single most reassuring piece of the cascading
  test.
- **A live provider outage (a transient DNS failure to api.openai.com
  inside this dev environment) surfaced as a clear, honest, visible
  failure message with a retry button** — not a hang, not a generic
  error, the real underlying error text was shown to the user.
- **Room deletion is genuinely complete**, verified directly against the
  database: rooms, messages, agents, and events all returned zero rows
  after deletion, not a soft-delete or UI-only removal.
- **Custom instructions reliably and exactly shape every reply** — a
  deliberately distinctive test phrase appeared verbatim at the end of
  every subsequent agent message across multiple rooms, no exceptions.
- Small polish details that add up: the dashboard's empty-state copy
  varies and gets time-of-day/name-aware ("Good evening, Kingsley")
  rather than staying static; masked API key display in Settings shows
  only the last few characters; Settings modal is honest about
  what's not built yet ("Team & roles aren't built yet... a real,
  planned phase") instead of a dead link.

---

## Friction points

Ordered roughly by how often a real user would hit them.

1. **Typing "@" in the message box does nothing.** The natural,
   Slack/Discord/ChatGPT-trained instinct is to type `@AgentName` and
   get an autocomplete. Nothing happens — the real mention mechanism is
   an entirely separate UI element, the "Address:" chip row above the
   composer. It works well once discovered, but nothing in the UI hints
   that the two are different, and the placeholder text ("@mention an
   agent to address it") actively suggests the wrong mechanism.
   *Repro: open any room with 2+ agents, click the message box, type
   "@". No dropdown appears.*

2. **A follow-up message with no explicit mention silently goes
   nowhere, with zero feedback.** In a room with 2+ agents, replying to
   an agent's own message the way you naturally would to a person who
   was just talking to you produces no error, no visual marker on the
   sent message, and no reply — just silence. This is correct per the
   product's own design (explicit mention required once a second agent
   exists), but the complete absence of any "this wasn't addressed to
   anyone" signal makes it feel broken rather than intentional.
   *Repro: 2-agent room, get a reply from @AgentA, then send a plain
   follow-up with no mention selected. Wait — nothing happens, ever.*

3. **The sidebar's room "⋮" actions menu (Pin/Rename/Delete) needed a
   hover-then-two-clicks pattern to actually open**, reproduced three
   separate times across different rooms: the first click after
   hovering onto a not-yet-focused row visibly did nothing, the second
   click on the same spot opened the menu.

4. **Renaming a room updates the sidebar instantly but not the open
   room's own header**, which keeps showing the old title until a full
   page reload. Confirmed by renaming, clicking away and back into the
   same room in-app (still stale), then navigating fresh (correct).

5. **Cascading plus the models' own tool habits produces real cleanup
   debris with no visibility into how much of it exists.** One short
   cascading exchange left 3 unresolved `request_handoff` approval
   cards sitting in a room's timeline (never touched by a human) and a
   genuine duplicate task (two rows, identical objective, because a
   second agent created its own task mid-cascade instead of using the
   one that already existed). There is no room-wide "N approvals
   pending" indicator anywhere — each sits as its own inline card, only
   discoverable by scrolling the full timeline.

6. **On mobile, the composer's address quick-select row sits close
   enough to the message textarea that a tap meant for the text field
   can land on an "@AgentName" chip instead**, silently toggling it
   into "addressed" state. Real mis-tap risk on a narrow screen.

7. **The public-facing marketing footer (visible on the logged-out
   `/login` page) is full of dead links** — "Documentation," "Careers,"
   etc. all point at `href="#"` and do nothing when clicked. This
   sits in visible contrast to the in-app Settings modal, which handles
   its own not-yet-built sections honestly (a real "soon" label plus a
   real explanatory empty state).

8. **Custom instructions can leak into auto-generated room titles.** A
   distinctive marker phrase appended to every reply via custom
   instructions ended up verbatim inside an auto-generated room title
   ("Greeting ChatGPT 2 Beep boop, agent out") — the title generator
   doesn't appear to distinguish real conversational content from a
   boilerplate suffix that's present on literally every message.

---

## Reached for and didn't find

- **A way to actually accept a proposed handoff.** See the bug below —
  this is the single biggest "reached for it, it's not there" moment
  of the whole pass. The approval flow (propose → human approves) is
  fully real; the second half (the receiving agent picking the work up)
  has no path anywhere in the product.
- **Any at-a-glance indicator of pending approvals or duplicate work**
  across a room — everything about cascading's aftermath has to be
  discovered by scrolling and reading, there's no summary state
  anywhere (sidebar badge, Room Info count, header indicator).
- **Any UI distinction for an agent message with empty content** —
  when a tool-call-only turn produces no visible text, it renders as a
  literally blank message bubble (agent name + timestamp, nothing
  else) rather than a placeholder like "used a tool, no reply text."
- **Team & roles, Security, Billing, Notifications** — all explicitly
  labeled "soon" in Settings, consistent with "Harmonia is single-user
  for now." Not a gap, just noting these were reached for as part of a
  normal settings sweep and are genuinely not there yet, honestly.

---

## Actual bugs (something broke, not just something clunky)

1. **Agent self-mention cascade loop, burning real spend on empty
   output.** Confirmed via direct database inspection (message content
   length, real non-zero token counts — not a rendering artifact). A
   single human message asking an agent to bring in the other agent via
   `mention_agent` produced a chain where the *second* agent mentioned
   **itself** twice in a row before finally producing real content:

   ```
   ChatGPT   (reply to human)      content_len=0, 19 output tokens
     -> ChatGPT 2 (cascaded)       content_len=0, 19 output tokens
       -> ChatGPT 2 (self-cascade) content_len=0, 19 output tokens
         -> ChatGPT 2 (real reply) "Hi there! I'm here and ready..."
   ```

   Nothing in the mention resolution path appears to filter an agent
   naming itself as a mention target. This resolved on its own after 2
   wasted hops here, but a slightly different model response could
   plausibly keep self-mentioning all the way to the 3-hop cap, burning
   the maximum cascade spend and ending in the depth-cap message
   without ever producing a real answer. The three empty-content
   messages render as blank chat bubbles with no explanation.

2. **A proposed-and-approved handoff has no way to actually complete.**
   Approving a `request_handoff` proposal creates a `handoffs` row with
   status `REQUESTED` and nothing more. A real `Accept` mechanism
   exists in the backend (`internal/handoff.Store.Accept`, an atomic
   `REQUESTED`→`ACCEPTED` transition, a mounted `POST
   /v1/handoffs/{id}/accept` route, a real `HANDOFF_ACCEPTED` event
   type) — but nothing in the entire frontend calls that endpoint, and
   no agent-facing tool exists for an agent to accept a handoff on its
   own. A handoff can only ever reach `REQUESTED` through the product
   as it exists today; it is a structural dead end, not a timing issue
   (confirmed via direct query — the row was still `REQUESTED` after
   6+ seconds of a "Live" room with no further activity).

3. **`create_task` narration without an actual tool call**, on a real,
   moderately complex first attempt. The model wrote "Now, I will
   create a task... Creating the task now..." in plain text and never
   actually called the tool — confirmed via a direct `SELECT * FROM
   tasks` returning zero rows. An explicit follow-up nudge ("don't just
   describe it, call the tool") fixed it reliably. The same tool fired
   correctly and immediately in a separate, simpler single-message
   context (autonomous pickup), suggesting this is a reliability
   gradient tied to turn complexity, not a broken mechanism — see the
   `RequireToolCall` doc comment and ADR-006's "Known limitation"
   section, which already names this exact class of problem.

4. **Duplicate tasks aren't prevented or flagged.** A second agent,
   mid-cascade, created a brand-new task with the identical objective
   text as one that already existed and was already `QUEUED`, rather
   than referencing or updating it. Two full rows in `tasks`, same
   text, no dedup logic anywhere.

5. **Pinned decisions render raw, unrendered markdown in Room Info.** A
   decision pinned from a citation-bearing reply shows literal
   `**Sources**` and `([url](url))[1]` syntax as plain text — brackets,
   asterisks, and all — in the Decisions panel, even though the exact
   same content rendered correctly (bold text, real clickable links) in
   the original timeline message. The Decisions panel isn't running
   pinned content through the same markdown renderer the timeline uses.

---

## The actual question: is this better than two ChatGPT tabs?

**Yes, plainly — for the specific thing it's actually good at, which is
narrower than "replaces manual multi-agent coordination" but real.**

Autonomous pickup and both search paths are the clearest wins: sending
one unaddressed message and having the right agent quietly pick it up,
do real work, and produce a real task — with no copy-pasting context
between tabs, no manually deciding who should handle what — is
meaningfully better than the two-tabs baseline. That's a genuine
capability a human relaying context by hand doesn't get, and it worked
cleanly, not just technically. Same for search: getting a real, cited,
grounded answer inline in the same conversation the engineering
discussion is happening in, with two independently-working trigger
paths, beats tabbing over to a browser and back.

Where it does **not** yet clear that bar is the actual founding
scenario this pass was built around — one agent delegating structured
work to another via a real handoff. That flow looks complete in the UI
(a real proposal card, a real approval card, a real "REQUESTED" status)
right up until the point where a human would expect the second agent to
actually start working, and instead nothing happens, forever. Manually
relaying "hey, can you review this" between two ChatGPT tabs is
*slower* than this, but it actually completes. Today, the automated
version doesn't. Cascading has the same shape of problem one level
down: it's real, it's bounded (the depth cap genuinely works), but what
it produces in practice — duplicate tasks, a pile of untouched approval
requests, agents defaulting to the same handoff/task-creation reflexes
rather than substantively building on each other's work — is closer to
process overhead a human then has to clean up than to the "two agents
review each other's work" pitch on the dashboard's own empty state.

The honest summary: for narrow, single-agent, single-turn delegation
(pickup, search, file-grounded Q&A), Harmonia is a genuine improvement
over manual tab-relaying. For the multi-agent handoff scenario the
product's own onboarding hints and the founding example both point to,
it currently produces more overhead than it saves, because the one
mechanism (`request_handoff`) that's supposed to make that scenario
real has no way to finish.

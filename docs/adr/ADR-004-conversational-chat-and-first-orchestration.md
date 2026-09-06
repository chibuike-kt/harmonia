# ADR-004: Conversational chat loop — messages, @mention invocation, first real orchestration path

**Status:** Accepted
**Date:** 2026-09-05

## Context

`messages` has been a known, named gap since the Milestone 1 design doc
(section 12's deferred list), raised again twice since. Separately,
`credentials.Store.Resolve` has sat fully built and tested with no
production caller — every report on it noted the orchestration engine
that would call it "doesn't exist until a later phase." This ADR is that
phase arriving: a human can now type a message to a specific agent and
get a real, generated reply.

## Decisions

**@mention only, structured — not text parsing.** An agent responds only
when explicitly addressed. The mention is a structured field
(`mentioned_agent_id`) the UI sets via an @-picker, not a regex scanning
message text for `@Name` — name collisions and typos make text parsing
fragile for something that triggers a real, billable API call.

**v1 scope: prove the loop with one agent, but don't hard-code it.** The
invocation logic responds to whichever agent is mentioned — it never
assumes "the room's only agent." Multi-agent rooms work by construction
once they exist; this phase just doesn't build UI/testing depth for that
case yet.

**Chat is additive, not a replacement.** Messages render interleaved
with existing task/handoff/event cards in the same room timeline —
exactly the three-grammar structure (agent messages, task cards, handoff
events) described the first time this project designed a room view.
Nothing about the structured AACP task/handoff system goes away.

**Chat does not trigger structured actions yet.** An agent's reply is
plain generated text. It does not create tasks, claim work, or request
handoffs as a side effect of a conversation. That's real tool-use/
function-calling scope — a genuinely later phase, not something to fold
in here under "while we're at it."

**Invocation is asynchronous.** Posting a human message returns
immediately (201) with the message stored and published live. The
mentioned agent's reply is generated in the background and delivered as
a new message once ready — blocking the request on an LLM call would
make the UI hang for however long generation takes.

**Context assembly, v1: a plain recency window.** The last N messages in
the room, formatted as a conversation, is what the agent sees. Not the
original product doc's relevance/dependency-ranked context engine —
that's deferred, same discipline as everywhere else in this build.

**Presence is the typing signal — no new status value.** `agents.status`
already flips `running`/`available` on task claim/complete. The same
mechanism, reused, is what drives a real typing indicator for the first
time — the orb finally reflects genuine in-flight work, not a static
dot.

**Failures are visible messages, never silent.** A failed generation
(bad key, rate limit, provider error) posts a real, distinguishable
message explaining what happened. A human who @mentions an agent gets a
visible answer either way.

## Revisit When

Multi-agent rooms need real turn-taking beyond "whoever's mentioned
responds" (an agent speaking up unprompted, for instance), or when a
conversational reply needs to actually trigger a structured action —
that's the tool-use phase, not this one.

## Addendum (2026-09-05): native provider tools change the tool-execution calculus

Initial planning for this phase assumed enabling web search or file
access would mean Harmonia building and hosting that infrastructure
itself — real sandboxing, real prompt-injection defense against raw
scraped content. That assumption was wrong and is worth correcting here
rather than leaving it only in chat history.

Both Anthropic (`web_search_20250305` as a Messages API tool) and OpenAI
(`web_search`/`file_search` as Responses API tools) already run these as
**server-side tools on their own infrastructure** — the provider decides
when to search, executes it, and returns cited results already
integrated into the response. Harmonia never touches raw web content
directly; that trust boundary lives entirely on the provider's side,
already engineered for exactly this. "Research" (multiple searches in
one turn) isn't a separate feature to orchestrate — it's what the model
already does on its own when given the tool and room to use it.

This means web search and simple single-message file attachment
(passing a file's content inline in a request, not a persistent library)
are **small, well-scoped, near-term work** — not the heavier Phase 4
tool-execution vision. Code execution remains deliberately later: not
because it's unsafe in the way raw web content would be, but because
it's a genuinely bigger step in cost and blast radius, worth its own
decision rather than bundling in because it came up in the same
conversation.

One real implementation wrinkle: Harmonia's OpenAI client (built for the
Milestone 1 acceptance test) uses Chat Completions; OpenAI's web search
tool is Responses-API-only. Enabling it for OpenAI means a real, if
contained, client change — not just a flag flip. Anthropic's client
needs less structural change, since it's the same Messages API endpoint
already in use.

**Sequencing:** this phase (the 1:1 conversational loop) stays scoped
exactly as already decided — no tool wiring here. Native web search
and simple file attachment are the very next phase once this one is
built and reviewed, not an indefinite "someday."

## Addendum (2026-09-06): rooms are created nameless, named from their first message

Creating a room used to mean typing a name before anything could
happen — a real point of friction once chat exists, and inconsistent
with how every LLM chat product actually works (you start typing, the
title appears on its own). Changing this:

**Room creation no longer requires a name.** `POST /v1/rooms` accepts
`name` as optional; when omitted, the room is created with a literal
placeholder ("New room") and the person lands directly in the empty
room — no intermediate form.

**The title is generated from the room's first message, once it
exists.** After the first message in a room is stored (human or
agent — whichever happens to land first), an async job (same shape as
the reply-invocation orchestrator, not the same code path) resolves
one of the room owner's own connected BYOK credentials — this is a
platform-level utility call, not tied to any room-scoped agent, since
a fresh room may not have one registered yet — and generates a short
title from that message's content.

**The generated title never overwrites a real rename.** Before writing
the generated name, the job re-reads the room's current name and only
applies the generated title if it's still the placeholder. A human who
manually renamed the room in the meantime wins, unconditionally — this
guards a real race between a slow title-generation job and a fast
manual rename.

**The reveal is a frontend animation over a complete string, not real
token streaming.** Consistent with the reply-generation decision
earlier in this document (post the complete reply, don't stream
tokens) — the backend returns the finished title in one piece; the
"typing" effect is a simulated character-by-character reveal on the
frontend, not a live stream from the model.

**This is a real, if small, API call, same as everything else on a
BYOK account.** Worth being honest about it existing rather than
treating it as free — it's one short completion per room, not
per-message.

## Addendum (2026-09-06): what's explicitly NOT part of this update

Three things from the room mockup's later design pass are real,
worthwhile, and each bigger than a UI addition — none are in scope
here:

- **The room decisions/info panel** needs an actual capture
  mechanism (how does a "decision" get created — manual pinning by a
  human, or agent-proposed and human-approved per the original product
  doc's ADR concept?) before it can be more than static mockup content.
  Undesigned, not just unbuilt.
- **The cost/token pill** needs real usage tracking — capturing
  token counts from every `Generate` response and persisting them
  (the original product doc's `usage_records` table was named years
  ago and never built). A real, contained piece of backend work, but
  its own piece.
- **The approval-required card** has nothing to approve yet — it
  illustrated a future capability (an agent proposing an action) that
  doesn't exist, since chat still doesn't trigger structured actions
  per this document's own existing decision. Building the card without
  the underlying capability would be UI theater, not a feature.

Each is real future work, each deserves its own design pass when its
time comes — not a quiet, partial version bundled into this update.

# Dogfooding fix brief — for Claude Code

**Read first:** `docs/dogfooding-findings.md` (the full report),
`docs/adr/ADR-006-multi-agent-group-dynamics.md`'s newest addendum
(handoff auto-accept — read this before touching item 1).

Three batches, stop between each — P0 is the founding scenario actually
working, P1 is real bugs with cost/trust consequences, P2 is a longer
list of contained friction fixes that can be batched and reported
together.

## Batch P0 — the founding scenario

1. **Handoff auto-accept on approval.** Per ADR-006's newest addendum:
   when an `agent_action_proposals` row for `request_handoff` gets
   approved, execute `handoff.Store.Request` and immediately transition
   it to `ACCEPTED` in the same flow — not two separate steps a human
   has to trigger. Real proof: approve a proposal, confirm the resulting
   `handoffs` row is `ACCEPTED` with zero further action, live.

2. **Self-mention cascade fix.** `mention_agent`'s valid targets must
   exclude the invoking agent's own ID — both at tool-declaration time
   (don't offer an agent itself as a choice) and as a defensive check at
   execution time, in case a model names itself anyway. The dogfooding
   report's repro (an agent mentioning itself twice before producing
   real content, three real empty-content messages burned) is your test
   case — write a deterministic test that reproduces it, confirm the
   fix prevents it, same standard as every other loop-prevention test in
   this project.

**Stop. Report with real proof for both — a completed handoff with no
manual step, and a self-mention attempt that's correctly blocked.**

## Batch P1 — real bugs, real cost/trust consequences

3. **Duplicate task prevention.** Same fix shape already used for
   `request_handoff`'s own tool description (which lists real open
   tasks and real other agents rather than letting the model invent
   references) — `create_task`'s description should enumerate the
   room's current open tasks, so a model has visibility to avoid
   creating a near-duplicate of one that already exists mid-cascade.

4. **Pinned decisions render raw markdown.** The Decisions panel isn't
   running pinned content through the same renderer the timeline
   already uses. Fix at the render layer — reuse the existing component,
   don't build a second markdown path.

5. **`create_task` narration without a real tool call, on complex
   turns.** Already a named, accepted limitation (ADR-006's "Known
   limitation" section) — don't try to force tool calls broadly, that's
   explicitly the wrong fix. Instead: detect the pattern (narration
   text describing task creation with no corresponding tool call in the
   same response) and automatically retry once with reinforced
   instruction before surfacing anything to the user. State plainly if
   the detection heuristic has false-positive risk.

**Stop. Report with real proof for each — a cascade that doesn't
duplicate a task, a pinned decision that renders correctly, and the
retry mitigation actually recovering a real narration-without-tool-call
case.**

## Batch P2 — friction, batch together, one report

6. **Real `@` autocomplete in the composer's text input.** Typing `@`
   should open a filterable picker of the room's agents — matching what
   every reference product trains a user to expect — rather than
   nothing happening while a separate, undiscoverable "Address" chip row
   is the only real mechanism. Selecting an agent from the picker should
   produce the same effect as the existing Address mechanism, not a
   second, parallel one.

7. **Visible feedback when a message addresses nobody in a 2+-agent
   room.** This is correct, intentional behavior (explicit mention
   required) — the problem is total silence makes it feel broken. Add a
   small, non-blocking inline marker on a sent message that addressed no
   one.

8. **Room header doesn't update live on rename** — same "reconstructed
   vs. live" mismatch class this project has hit before. Subscribe the
   header to the same rename event the sidebar already correctly
   listens to.

9. **A pending-approvals indicator**, visible at a glance — a count
   somewhere real (Room Info, or the header), not something only
   discoverable by scrolling the full timeline.

10. **Sidebar "⋮" menu's hover-then-two-clicks bug.** Check for the same
    class of Tailwind stacking-context issue found twice already in this
    project (a `translate`/`transform` utility silently creating a new
    stacking context) before assuming a different cause.

11. **Mobile mis-tap risk** between the composer's address-chip row and
    the text field — increase spacing/hit-target separation at narrow
    widths specifically.

12. **Marketing footer's dead links** (`href="#"` on Documentation,
    Careers, etc.) — apply the same honest treatment Settings already
    uses for its own not-yet-built sections: either remove the link or
    label it plainly as not yet real, never a silent dead click.

13. **Custom instructions leaking into auto-generated titles.** Title
    and objective generation summarize conversation content — they were
    never supposed to be fed the user's standing reply-style
    instructions in the first place. Exclude `custom_instructions` from
    that specific prompt path.

Report on all of P2 together once every item is verified — this batch
doesn't need the same per-item ceremony as P0/P1, but every fix still
needs real verification, not just a code change.

## Definition of done

A handoff, once approved, completes with no further action. An agent
cannot cascade-mention itself. Cascading no longer produces duplicate
tasks. Decisions render correctly. The composer's `@` behaves the way
every user's instincts already expect. Nothing in P2 regresses anything
already working — same standard as every prior round.

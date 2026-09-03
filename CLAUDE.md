# Working in this repository

Harmonia coordinates real handoffs between AI agents and is the audit trail
both the agents and the humans behind them rely on. The code here is meant
to be read and trusted by senior engineers. Hold that bar.

## Version control — hard rules

- **Never run `git commit`, `git push`, or any command that writes to history.**
  Staging is fine for inspection; committing is the maintainer's job, always.
- **Never add a co-author trailer, `Co-Authored-By`, or any attribution to an AI
  tool** in commit messages, code comments, or documentation.
- Do not create, amend, rebase, or tag. Leave the history entirely to the human.

If a change is ready, say so and stop. The maintainer commits it.

## Commits are atomic (guidance for the maintainer)

One feature or fix per commit. Do not batch unrelated changes. A commit should
compile and pass its own tests in isolation. Message style: imperative subject,
present tense, no trailing period — e.g. `add atomic claim query to task store`.

## Code standards

- **Idiomatic per language.** Go reads like Go. If a second language ever
  enters this repo, it earns that place through an ADR first (see below) —
  and it reads like itself, no cross-language accents.
- **Comment sparingly.** Explain *why*, never *what*. No comment restating the
  code on the next line. No docstring on a self-evident function. If a comment
  would only narrate the obvious, delete it.
- **No AI tells.** No "Here's the…", no over-explained blocks, no defensive
  hedging in prose. Write like an engineer who knows the domain.
- **Errors are handled, not swallowed.** On the task/event path, an unhandled
  edge is a bug, not a TODO.
- **Events are append-only and immutable.** No `UPDATE` or `DELETE` against
  the events table — enforced at the database role level, not just by
  convention. If a package needs to correct an event, it appends a
  correction, it doesn't mutate history.
- **State transitions are atomic.** Task claims, handoffs, and status changes
  use conditional writes (`WHERE status = ...`), never read-then-write. Two
  agents racing for the same task is the normal case, not an edge case.
- **Protocol messages are versioned.** Never change the AACP envelope shape
  or an operation's payload contract without bumping `protocol_version`.
  Adding a new operation is fine; changing an existing one silently is not.

## Tests

Every package ships its own tests. Code must compile and its tests must pass
before it's considered done. No feature is done without a test that would
fail if the behavior regressed — for anything touching task claims or
handoffs, that includes a concurrency test, not just a happy-path unit test.

## Scope discipline

Build to the current milestone, not the full vision. If a change reaches for
something the active milestone's design doc lists as deferred, that's a
signal to stop, not to widen the change. Decisions about language, protocol,
or storage boundaries that outlive a single PR get recorded as an ADR in
`docs/adr/`, not left in chat history.

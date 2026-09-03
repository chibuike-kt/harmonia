# Contributing to Harmonia

Thanks for considering a contribution. Harmonia coordinates real handoffs
between AI agents — the bar for what goes in is deliberately high. This
document covers how the project is built and how to get a change merged.

## Ground rules

- Discuss non-trivial changes in an issue first. A new AACP operation or a
  new internal package especially should start as a short written proposal,
  not a PR that introduces it as a side effect.
- Keep changes focused. One concern per branch, one logical change per
  commit.
- Do not add AI-tool attribution — no `Co-Authored-By` trailers, no
  generated "authored by" notes in commits, code, or docs. Authorship stays
  human.

## Branching model

Harmonia uses trunk-based development (GitHub Flow). `main` is always
releasable and protected; all work happens on short-lived branches off
`main` and returns through a pull request.

Branch names carry a type prefix and a short kebab-case description:

```
feat/handoff-accept-endpoint
fix/event-payload-nullable
docs/adr-002-sandbox-execution
refactor/store-pool-wiring
test/handoff-accept-race
chore/ci-cache
```

## Commits

Commits follow [Conventional Commits](https://www.conventionalcommits.org).

```
<type>(<optional scope>): <imperative summary>
```

Types: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`, `perf`.

```
feat(task): add atomic claim query to task store
fix(event): guard against nil payload on insert
docs: record ADR-002 for sandbox execution boundary
```

A commit should build and pass its own tests in isolation. Subject line in
the imperative mood, no trailing period, under ~72 characters. Explain the
*why* in the body when it isn't obvious.

## Pull requests

1. Branch off the latest `main`.
2. Make atomic commits as you go.
3. Open a PR into `main`. Fill in the template.
4. CI must pass — vet, lint, and race tests against live Postgres/Redis are
   required checks.
5. Self-review the diff as if it were someone else's. Then merge.

Feature branches are integrated with **merge commits** (`--no-ff`), which
keep each atomic commit and preserve the branch boundary in history. Do not
squash — it collapses the granular history the project intentionally keeps.

## Local development

```sh
cp .env.example .env        # fill in ANTHROPIC_API_KEY / OPENAI_API_KEY
make up                     # Postgres + Redis via docker-compose
make migrate-up             # requires the golang-migrate CLI

make run                    # run the server
make lint                   # matches CI; requires golangci-lint v2
make test-race               # unit + race tests
make test-integration       # requires `make up` first
```

The repository pins its lint configuration to v2 in
[`.golangci.yml`](.golangci.yml). Run `go mod tidy` before your first build
to resolve current dependency versions — don't assume `go.mod`'s pinned
versions are still the latest compatible ones.

## Standards for code in this repo

- Idiomatic per language. Go reads like Go for now — a second language
  enters only behind an ADR (see `docs/adr/`), and it reads like itself, no
  cross-language accents.
- Comment the *why*, never the *what*. Delete comments that restate the
  code.
- Events are append-only. No `UPDATE` or `DELETE` against the events table
  — enforced at the database role level, not just convention.
- State transitions are atomic. Task claims and handoff status changes use
  conditional writes, never read-then-write.
- Protocol messages are versioned. A new AACP operation is additive; an
  existing operation's payload contract does not change without a
  `protocol_version` bump.
- Every package ships its own tests, and the repo builds with tests passing
  after any change — no exceptions for "will fix in a follow-up."

## Adding an internal package

New domain logic lives under `internal/`, following the existing pattern: a
pool-backed `Store` struct with narrow, purpose-built methods — not a
generic repository layer. A package earns a place in the tree when the
active milestone's design doc calls for it; if it reaches ahead of that,
open the proposal discussed above first, don't fold it into an unrelated PR.

## Adding an AACP operation

New operations extend the `Operation` consts in `internal/protocol` and get
documented in the design doc alongside the existing operation set, not left
implicit in code. The envelope shape (`internal/protocol/envelope.go`) does
not change to accommodate a new operation — if it looks like it needs to,
that's a signal the change belongs in a protocol version bump, discussed
first.

## License

Not yet decided for this repository — Keel's MIT license doesn't
automatically carry over, since Harmonia isn't necessarily open source.
Confirm and add a `LICENSE` file (and this section) before accepting
outside contributions.

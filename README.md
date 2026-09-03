# Harmonia

Agent collaboration platform — multiple AI agents share persistent project
state, delegate tasks, and hand work off to one another through a
structured protocol (AACP) instead of through a human relaying messages.

This repo currently targets **Milestone 1 only**: prove that two agents can
complete a task handoff through shared state with no human relaying context
between them. See `docs/adr/ADR-001-language-decision.md` for why this is
Go-only for now, and the Milestone 1 design doc for full scope and the
acceptance test this build is working toward.

## Local setup

```sh
cp .env.example .env      # fill in ANTHROPIC_API_KEY / OPENAI_API_KEY

docker compose up -d postgres redis      # start dependencies
docker compose run --rm migrate up       # apply migrations
docker compose up -d --build api         # build and run the server
```

Confirm it's up:

```sh
curl localhost:8080/healthz
```

Useful follow-ups:

```sh
docker compose logs -f api                 # tail server logs
docker compose run --rm migrate down 1     # roll back one migration
docker compose down                        # stop everything (data persists in the pgdata volume)
docker compose down -v                     # stop and wipe the Postgres volume too
```

A `Makefile` also exists as an optional shortcut for these (`make up`, `make migrate-up`, `make run` runs the server on the host instead of in a container) — use whichever you prefer; both point at the same `docker-compose.yml`.

## Verify before building on top of this

`go vet`/lint/test run on the host, not in the `api` container — its final
image is just the compiled binary with no Go toolchain in it, by design
(smaller image, smaller attack surface).

```sh
go mod tidy                          # resolve and verify latest compatible dependency versions
go vet ./...
go test ./... -race

docker compose up -d postgres redis
docker compose run --rm migrate up
go test ./... -run Integration -v    # against the running Postgres/Redis
```

Run `go mod tidy` before the first build — the dependency versions in
`go.mod` were current as of this scaffold's creation and should be
re-verified against the latest compatible releases at build time, per
usual practice.

## Layout

```
cmd/server/       entrypoint, HTTP wiring
internal/
  room/ task/ agent/ event/ handoff/ contextengine/   domain packages
  protocol/        AACP v0 message envelope
  provider/        model-agnostic Agent interface + anthropic/openai clients
  store/           shared Postgres pool + Redis client
migrations/        schema, append-only events table
docs/adr/          architecture decision records
tests/
  concurrency/     task-claim race test (the one that must never flake)
  integration/     reserved for broader Postgres-backed integration tests
```

## What's deliberately not here yet

Rooms UI, memory promotion, tool execution/sandboxing, event bus, semantic
memory, SDK, protocol binary encoding, lease expiry, multi-org tenancy.
Each has a phase in the original product overview — none of them are on
the critical path to Milestone 1.

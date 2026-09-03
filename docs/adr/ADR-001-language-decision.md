# ADR-001: Go only for Milestone 1, Rust deferred

**Status:** Accepted
**Date:** 2026-09-03

## Context

Harmonia's long-term vision includes a sandboxed agent execution layer and a
protocol transport layer, both plausible candidates for Rust. The question
was whether to introduce Rust starting with Milestone 1 (two-agent handoff,
no tool execution, no untrusted code).

## Decision

Build Milestone 1 entirely in Go.

## Reasoning

- No concurrency load in this milestone exceeds what goroutines + Postgres
  row-level locking handle (see task claiming, migrations/0001_init.up.sql).
- The strongest real argument for Rust — memory-safe sandboxing of
  agent-generated code — has no subject yet. There is no code execution in
  Milestone 1.
- The binary-transport argument requires benchmarking against real traffic,
  which doesn't exist yet.
- A single Go toolchain keeps this codebase directly compatible with Keel's
  existing fintech modules (idempotency, audit trail, txnstate), rather than
  requiring an early port.

## Revisit When

The sandbox/tool-execution phase becomes real scoped work, with an actual
threat model driving the decision — not before.

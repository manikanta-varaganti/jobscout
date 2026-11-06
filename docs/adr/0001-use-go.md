# ADR 0001: Implement in Go

Status: accepted

## Context
Goals: easy install for non-developers on macOS and Windows, efficient concurrent scraping, low memory, and a
long-running local server that agents can call. The first prototype was Python.

## Decision
Use Go. It produces one static binary per OS from a single CI job, has cheap concurrency (goroutines), and the
pure-Go SQLite driver (`modernc.org/sqlite`) removes the need for a C toolchain, so cross-compiling is trivial.

## Consequences
- Scraping throughput is bounded by the network and per-host politeness limits, not the language; the gain is distribution, memory and concurrency ergonomics.
- Contributors need Go. Provider code is small and fixture-tested to keep that barrier low.

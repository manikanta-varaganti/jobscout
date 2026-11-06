# ADR 0002: SQLite (WAL + FTS5) as the datastore

Status: accepted

## Context
Requirements: persistent across restarts and reboots, zero install, fast full-text and boolean search, safe
concurrent reads and writes from the UI, CLI, scanner and agents, simple backup.

## Decision
One SQLite file in WAL mode. FTS5 provides search. One writer connection plus a read-only pool. Agents reach the data
through the application (MCP/HTTP), never by opening the file, so validation, auditing and write serialisation stay in one place.

## Alternatives considered
- PostgreSQL: better for many concurrent writers or hosting, but a service to install and operate. Storage sits behind `internal/store`, so it can be swapped later.
- DuckDB: excellent for analytics, weak for frequent small upserts.
- JSONL/Parquet files: no indexes or upserts; forced the dedupe and state workarounds of the prototype.

## Consequences
- Single writer: fine at this scale (tens of thousands of rows, writes in short per-board transactions).
- The file must live on a local disk, not a network share.

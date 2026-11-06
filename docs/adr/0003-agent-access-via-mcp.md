# ADR 0003: Agent access through MCP, read-only by default

Status: accepted

## Decision
Expose the datastore to agents through an MCP server (stdio) and the same REST API. Write tools (`set_job_status`,
`add_job_tag`, `remove_job_tag`, `add_job_note`, `run_scan`) exist only with `--allow-write`. Every write records its actor in the
`events` table. A cursor-based change feed (`list_changes`) lets agents resume without re-reading everything.
Outbound actions (submitting applications, sending messages) are deliberately not tools.

## Consequences
- Job text is third-party content and may contain prompt-injection attempts. Tool results carry an explicit notice, and no tool can cause an external side effect.
- Search results omit descriptions by default to keep agent context small.

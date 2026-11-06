-- Company display names, dead-board handling.
ALTER TABLE boards ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE boards ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE boards ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0;

-- Descriptions are stored zlib-compressed; the plain column is kept for rows written before this migration.
ALTER TABLE job_text ADD COLUMN description_z BLOB;

-- Saved searches: named filters with "new since last viewed".
CREATE TABLE saved_searches (
  id             INTEGER PRIMARY KEY,
  name           TEXT NOT NULL UNIQUE COLLATE NOCASE,
  params         TEXT NOT NULL,  -- JSON-encoded search parameters
  created_at     TEXT NOT NULL,
  last_viewed_at TEXT
);
INSERT INTO saved_searches (name, params, created_at)
VALUES ('Software & backend', '{"target_only":true}', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));

-- Cross-process leases (e.g. only one scan at a time across serve, mcp and cli).
CREATE TABLE locks (
  name       TEXT PRIMARY KEY,
  owner      TEXT NOT NULL,
  expires_at TEXT NOT NULL
);

-- Idempotency keys for retried writes from agents and clients.
ALTER TABLE events ADD COLUMN request_key TEXT;
CREATE UNIQUE INDEX events_request_key ON events (request_key) WHERE request_key IS NOT NULL;

CREATE INDEX jobs_board_title ON jobs (board, title);

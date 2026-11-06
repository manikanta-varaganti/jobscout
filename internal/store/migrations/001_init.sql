CREATE TABLE boards (
  ats             TEXT NOT NULL,
  slug            TEXT NOT NULL COLLATE NOCASE,
  added_at        TEXT NOT NULL,
  last_scanned_at TEXT,
  last_status     TEXT,            -- ok | not_found | error
  last_error      TEXT,
  etag            TEXT,
  job_count       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (ats, slug)
);

CREATE TABLE jobs (
  id              INTEGER PRIMARY KEY,
  ats             TEXT NOT NULL,
  board           TEXT NOT NULL COLLATE NOCASE,
  ext_id          TEXT NOT NULL,
  title           TEXT NOT NULL,
  location        TEXT NOT NULL DEFAULT '',
  workplace       TEXT NOT NULL DEFAULT '',
  employment_type TEXT NOT NULL DEFAULT '',
  department      TEXT NOT NULL DEFAULT '',
  team            TEXT NOT NULL DEFAULT '',
  compensation    TEXT NOT NULL DEFAULT '',
  url             TEXT NOT NULL,
  published_at    TEXT,
  first_seen      TEXT NOT NULL,
  last_seen       TEXT NOT NULL,
  closed_at       TEXT,
  content_hash    TEXT NOT NULL DEFAULT '',
  is_target       INTEGER NOT NULL DEFAULT 0,
  status          TEXT NOT NULL DEFAULT '',   -- user workflow: saved | applied | rejected | ...
  UNIQUE (ats, board, ext_id)
);
CREATE INDEX jobs_published  ON jobs (published_at);
CREATE INDEX jobs_first_seen ON jobs (first_seen);
CREATE INDEX jobs_open       ON jobs (closed_at, is_target);
CREATE INDEX jobs_board_open ON jobs (ats, board, closed_at);
CREATE INDEX jobs_status     ON jobs (status) WHERE status <> '';

-- Descriptions live apart from jobs so list queries never read large text.
CREATE TABLE job_text (
  job_id      INTEGER PRIMARY KEY REFERENCES jobs (id) ON DELETE CASCADE,
  description TEXT NOT NULL
);

-- Full-text index (contentless; rowid = jobs.id).
CREATE VIRTUAL TABLE jobs_fts USING fts5 (
  title, company, location, department, description,
  content = '', contentless_delete = 1,
  tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TABLE job_tags (
  job_id INTEGER NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
  tag    TEXT NOT NULL,
  PRIMARY KEY (job_id, tag)
);

CREATE TABLE job_notes (
  id     INTEGER PRIMARY KEY,
  job_id INTEGER NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
  ts     TEXT NOT NULL,
  actor  TEXT NOT NULL,
  note   TEXT NOT NULL
);
CREATE INDEX job_notes_job ON job_notes (job_id);

-- Change feed and audit log. Agents poll with "events since id".
CREATE TABLE events (
  id     INTEGER PRIMARY KEY AUTOINCREMENT,
  ts     TEXT NOT NULL,
  kind   TEXT NOT NULL,   -- added | updated | closed | reopened | status | tag | note
  job_id INTEGER,
  actor  TEXT NOT NULL,
  data   TEXT
);
CREATE INDEX events_job ON events (job_id);

CREATE TABLE scan_runs (
  id           INTEGER PRIMARY KEY,
  started_at   TEXT NOT NULL,
  finished_at  TEXT,
  boards_total INTEGER NOT NULL DEFAULT 0,
  boards_ok    INTEGER NOT NULL DEFAULT 0,
  boards_failed INTEGER NOT NULL DEFAULT 0,
  jobs_seen    INTEGER NOT NULL DEFAULT 0,
  jobs_added   INTEGER NOT NULL DEFAULT 0,
  jobs_closed  INTEGER NOT NULL DEFAULT 0
);

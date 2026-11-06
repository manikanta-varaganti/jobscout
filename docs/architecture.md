# Architecture

jobscout is a single Go binary. This page explains how it is put together, with a diagram for each main flow.
New to the project? Read [The big picture](#the-big-picture) and [Layers](#layers), then whichever flow you need.

**Contents:** [Big picture](#the-big-picture) · [Layers](#layers) · [A sync, step by step](#a-sync-step-by-step) ·
[How one job is applied](#how-one-job-is-applied) · [A search request](#a-search-request) · [Data model](#data-model) ·
[Concurrency](#concurrency) · [Security](#security-model) · [Extending](#adding-an-ats-provider)

## The big picture

```mermaid
flowchart LR
    subgraph internet["Internet (public job feeds)"]
        GH["Greenhouse"]; LV["Lever"]; AS["Ashby"]; WK["Workable"]; RC["Recruitee"]; SR["SmartRecruiters"]
    end
    subgraph machine["User's computer: one jobscout binary"]
        direction TB
        subgraph entry["Ways in"]
            UI["Web dashboard<br/>(embedded, served on 127.0.0.1)"]
            CLI["Command line"]
            MCP["MCP server<br/>(stdio, for AI assistants)"]
        end
        SVC["Service layer<br/>(scans, boards)"]
        ST["Store<br/>(SQLite access)"]
        DB[("jobscout.db<br/>WAL + FTS5")]
        SCR["Scrapers<br/>(one provider per platform)"]
    end
    UI --> SVC
    CLI --> SVC
    MCP --> SVC
    SVC --> ST --> DB
    SVC --> SCR
    SCR --> GH & LV & AS & WK & RC & SR
```

Three front doors (dashboard, CLI, MCP) share one service layer, so a rule such as "closed jobs are never deleted"
is enforced in one place no matter who is asking.

## Layers

Dependencies point **inward only**. Transports never talk to scrapers, and `domain` imports nothing.

```mermaid
flowchart TD
    main["cmd/jobscout<br/>signals · version · exit code"] --> cli
    cli["internal/cli<br/>command parsing"] --> http & mcp & svc & imp
    http["internal/transport/httpapi<br/>REST API + embedded UI"] --> svc
    mcp["internal/transport/mcp<br/>MCP over stdio"] --> svc
    imp["internal/importer<br/>legacy JSONL loader"] --> store
    svc["internal/service<br/>scan orchestration · lease · enrichment"] --> store & scrape
    scrape["internal/scrape<br/>providers · polite HTTP client · URL discovery"] --> domain
    store["internal/store<br/>migrations · writer/reader pools · search · events"] --> query & tf & domain
    query["internal/query<br/>boolean language → SQL"]
    tf["internal/titlefilter<br/>software/backend classifier"]
    domain["internal/domain<br/>shared types"]
    cfg["internal/config<br/>env · per-OS data dir"]
    cli --> cfg
    web["web/<br/>static UI (go:embed)"] -.served by.-> http
```

| Package | Responsibility |
|---|---|
| `cmd/jobscout` | Process entry point: signal handling, version, exit code |
| `internal/cli` | Commands (`init`, `scan`, `search`, `serve`, `mcp`, `board`, `saved`, `import`, `maintenance`, `backup`) |
| `internal/transport/httpapi` | REST API + embedded UI; Host guard, CSRF header, CSP, body limits, request IDs |
| `internal/transport/mcp` | MCP server; read-only by default; audit-logged writes |
| `internal/service` | Scan orchestration, cross-process lease, description enrichment, board management |
| `internal/scrape` | Providers (Ashby, Greenhouse, Lever, Recruitee, Workable, SmartRecruiters), HTTP client, HTML→text, URL canonicalisation, board-URL discovery |
| `internal/store` | SQLite: migrations, single writer, read pool, search, events, saved searches, locks, maintenance |
| `internal/query` | Boolean search language → SQL over the FTS index |
| `internal/titlefilter` | Software/backend title classifier (`is_target`) |
| `internal/importer` | Loader for the legacy Python JSONL snapshots |
| `internal/config` | Environment variables and the per-OS data directory |
| `internal/domain` | Shared types |
| `web/` | Static UI, embedded with `go:embed` |

## A sync, step by step

```mermaid
sequenceDiagram
    autonumber
    participant U as User / scheduler / agent
    participant S as Service
    participant L as locks table
    participant W as Worker pool (N)
    participant P as Provider
    participant X as ATS feed
    participant DB as Store (one writer)
    U->>S: StartScan
    S->>L: AcquireLock("scan", owner, 2 min)
    alt another process holds it
        L-->>S: refused
        S-->>U: ErrScanRunning (409 / "already running")
    else acquired
        S-->>U: 202 started
        S->>DB: ListBoards (enabled) + StartRun
        par for each board (N at a time, bounded per host)
            W->>P: Fetch(slug, previous ETag)
            P->>X: GET feed (If-None-Match)
            alt 304 Not Modified
                X-->>P: 304
                W->>DB: TouchBoard (refresh last_seen only)
            else 200
                X-->>P: listing (all pages)
                W->>DB: ApplyBoard: one transaction
                W->>P: (optional) company name, missing descriptions
                W->>DB: SetDisplayNameIfEmpty / SetDescription
            else error
                X-->>P: 404 / 429 / 5xx / timeout
                W->>DB: SetBoardResult(failure) and maybe disable after 5×404
            end
        and every 30 s
            S->>L: renew lease
        end
        S->>DB: FinishRun (totals)
        S->>L: ReleaseLock
    end
```

Key properties:
- **One failing company never fails the sync.** Failures are recorded per board; the run errors only if *every* board fails.
- **Politeness:** retries with backoff and `Retry-After`, a per-host concurrency cap, conditional requests, a response-size cap.
- **Cancellable:** `Service.Close` (Ctrl+C on `serve`) cancels the run and waits for it before the database closes.

## How one job is applied

`ApplyBoard` loads the board's existing jobs in **one query**, then decides per job inside a single transaction:

```mermaid
flowchart TD
    A["Job from the feed"] --> B{"Already listed earlier<br/>in this same feed?"}
    B -- yes --> SKIP["Skip duplicate"]
    B -- no --> C{"Known job?<br/>(ats, board, ext_id)"}
    C -- no --> ADD["INSERT job + compressed text + FTS row<br/>event: <b>added</b>"]
    C -- yes --> D{"Content hash changed?"}
    D -- yes --> UPD["UPDATE fields, text, FTS<br/>event: <b>updated</b> (or <b>reopened</b> if it was closed)"]
    D -- no --> E{"Was it closed?"}
    E -- yes --> RE["Clear closed_at<br/>event: <b>reopened</b>"]
    E -- no --> TOUCH["Update last_seen only"]
    ADD & UPD & RE & TOUCH --> F{"All feed jobs done and<br/>the listing was complete?"}
    F -- yes --> CLOSE["Every open job of this board that was<br/><b>not in the listing</b> → closed_at = now<br/>event: <b>closed</b>"]
    F -- no --> END([Commit])
    CLOSE --> END
```

"Complete" is false when the feed returned an empty list for a board that previously had jobs. That is more likely a
glitch upstream than every job closing at once, so nothing is closed in that case.

### Job lifecycle

```mermaid
stateDiagram-v2
    direction LR
    [*] --> Open: added
    Open --> Open: updated / seen again
    Open --> Closed: missing from a complete listing
    Closed --> Open: reopened
    note right of Closed: never deleted, maintenance may drop only the description text
```

## A search request

```mermaid
flowchart LR
    R["GET /api/v1/jobs?q=…&target=1&group=1"] --> P["Parse filters"]
    P --> C["query.Compile<br/>(text → AST → SQL with ? placeholders)"]
    C --> W["WHERE = structured filters<br/>AND FTS subqueries"]
    W --> L["ONE light query<br/>id, company, dates, title<br/>(read-only pool)"]
    L --> M["In memory:<br/>counts · facets · per-day chart ·<br/>sort · group · cursor page"]
    M --> F["Fetch full rows for<br/>the visible page only"]
    F --> J["JSON response"]
```

The full-text match runs **once** per request no matter how many counts and facets the page shows.

### The query language

```mermaid
flowchart LR
    T["Tokenizer<br/>words · #quot;phrases#quot; · field:value ·<br/>-neg · ( ) · OR / AND / NOT"] --> P["Recursive-descent parser<br/>binds loosest to tightest:<br/>OR, then AND (implicit), then NOT"]
    P --> A["AST"]
    A --> E["Emitter<br/>term → j.id IN (SELECT rowid FROM jobs_fts WHERE jobs_fts MATCH ?)<br/>ats:x → j.ats = ?"]
```

- Terms longer than three characters become FTS prefix queries (`agent` → `"agent"*`); shorter ones match whole tokens (`AI`).
- Plain words are restricted to the `title` column unless the scope is `tcl` (title, company, location) or `all`.
- Malformed input degrades gracefully: unbalanced parentheses close implicitly, stray operators are ignored, control
  characters are stripped. `FuzzSearch` checks that no input can make a search fail.

## Data model

One SQLite file in WAL mode (see [ADR 0002](adr/0002-sqlite-as-the-datastore.md)).

```mermaid
erDiagram
    BOARDS ||--o{ JOBS : "ats + slug"
    JOBS ||--|| JOB_TEXT : "description"
    JOBS ||--|| JOBS_FTS : "rowid = id"
    JOBS ||--o{ JOB_TAGS : has
    JOBS ||--o{ JOB_NOTES : has
    JOBS ||--o{ EVENTS : "change feed / audit"
    BOARDS {
        text ats PK
        text slug PK
        text display_name
        text last_status
        text etag
        int job_count
        int consecutive_failures
        bool disabled
    }
    JOBS {
        int id PK
        text ats
        text board
        text ext_id "unique with ats, board"
        text title
        text location
        text url
        text published_at
        text first_seen
        text last_seen
        text closed_at "null = open"
        text content_hash
        bool is_target
        text status "user workflow"
    }
    JOB_TEXT {
        int job_id PK
        blob description_z "zlib"
    }
    JOBS_FTS {
        text title
        text company
        text location
        text department
        text description
    }
    EVENTS {
        int id PK
        text ts
        text kind "added updated closed reopened status tag note"
        int job_id
        text actor "scanner ui mcp:*"
        text request_key "idempotency"
    }
    JOB_TAGS {
        int job_id
        text tag
    }
    JOB_NOTES {
        int id PK
        int job_id
        text actor
        text note
    }
    SAVED_SEARCHES {
        int id PK
        text name
        text params "JSON"
        text last_viewed_at
    }
    LOCKS {
        text name PK
        text owner
        text expires_at
    }
    SCAN_RUNS {
        int id PK
        text started_at
        text finished_at
        int boards_ok
        int jobs_added
    }
```

| Table | Purpose |
|---|---|
| `jobs` | one row per posting, unique on `(ats, board, ext_id)` |
| `job_text` | descriptions (zlib-compressed), kept apart so list queries never read large text |
| `jobs_fts` | FTS5 contentless index; the `company` column holds the slug and display name |
| `job_tags`, `job_notes` | user and agent annotations |
| `events` | append-only change feed and audit log |
| `boards` | boards to scan, with health and display name |
| `saved_searches` | named search parameters (JSON) and `last_viewed_at` |
| `locks` | cross-process leases (the scan lease) |
| `scan_runs` | history of scans |
| `schema_version` | applied migrations |

**Invariants**
- Jobs are never deleted. A job missing from a *complete* listing is marked `closed_at`; if it returns it is reopened.
- Closing is decided by membership in the listing, never by timestamps (two scans in the same second behave correctly).
- A `304 Not Modified` refreshes `last_seen` without rewriting rows.
- All timestamps are UTC RFC 3339 strings, so lexical comparison is chronological.
- A board is disabled after 5 consecutive `404`s; any successful scan resets the counter.
- A user-set company name is never overwritten by a name learned from a feed.

**Migrations** are numbered SQL files in `internal/store/migrations`, embedded in the binary and applied in order
inside a transaction at startup. Never edit a released migration; add a new one.

## Concurrency

```mermaid
flowchart LR
    subgraph p1["Process: jobscout serve"]
        H["HTTP handlers"]; S1["scan workers"]
    end
    subgraph p2["Process: jobscout mcp (agent)"]
        M["MCP tools"]
    end
    subgraph db["jobscout.db"]
        WAL["WAL"]
    end
    H -- "reads: query_only pool<br/>never blocked" --> WAL
    M -- reads --> WAL
    H -- "writes: single connection<br/>per process" --> WAL
    S1 -- "per-board transactions" --> WAL
    M -- "writes (if --allow-write)" --> WAL
```

- **Within a process**, all writes go through one connection, so they serialise without `SQLITE_BUSY` surprises.
  Reads use a separate pool of `query_only` connections that WAL lets run alongside the writer.
- **Across processes**, SQLite's file lock serialises writers (with a 10 s busy timeout), and the `scan` lease ensures
  only one sync runs at a time:

```mermaid
sequenceDiagram
    participant A as serve (owner A)
    participant L as locks table
    participant B as mcp (owner B)
    A->>L: acquire "scan" (expires in 2 min) ✔
    B->>L: acquire "scan" ✘ held by A
    B-->>B: report "running in another process"
    loop every 30 s while scanning
        A->>L: renew (owner A) ✔
    end
    A->>L: release
    Note over L: if A crashes, the lease expires after 2 min
    B->>L: acquire "scan" ✔
```

**Description enrichment.** Providers whose listings lack descriptions (SmartRecruiters) implement
`DescriptionFetcher`; after a board is applied the service fetches descriptions for at most 50 new jobs per board per
scan, so large employers fill in over several syncs without flooding the platform.

## Security model

```mermaid
flowchart LR
    Req["HTTP request"] --> RID["requestID<br/>X-Request-Id, logger"]
    RID --> REC["recoverer<br/>panic → 500"]
    REC --> HG{"hostGuard<br/>Host is loopback?"}
    HG -- no --> F1["403<br/>(DNS-rebinding defence)"]
    HG -- yes --> HDR["+ CSP, nosniff,<br/>no-referrer"]
    HDR --> CS{"csrf<br/>mutating request has<br/>X-Jobscout: 1?"}
    CS -- no --> F2["403"]
    CS -- yes --> LIM["1 MB body limit"] --> H["Handler<br/>strict JSON decoding"]
```

- The server binds to loopback; the Host check stops DNS-rebinding attacks from websites.
- Cross-site pages can't add the custom header without a CORS preflight, which the server never approves.
- Strict CSP: no inline scripts, no third-party origins. The UI renders scraped text with `textContent` only.
- CSV export prefixes cells beginning with `= + - @` so spreadsheets don't execute them.
- MCP write tools are off unless `--allow-write`; every write is attributed in `events`; job text is labelled untrusted for agents.
- The data directory is created `0700`.

## Adding an ATS provider

```mermaid
flowchart LR
    I["Implement scrape.Provider<br/>Name() · Fetch()"] --> O["Optional:<br/>BoardNamer · DescriptionFetcher"]
    O --> U["Teach ParseBoardURL<br/>its URLs"]
    U --> R["Register in<br/>scrape.Default()"]
    R --> T["httptest fixture test<br/>(no live network)"]
```

1. Implement `scrape.Provider` in `internal/scrape/<name>.go`. **`Fetch` must return the complete listing or an error,
   never a partial list**, because missing jobs are marked closed.
2. Optionally implement `BoardNamer` (company name) and `DescriptionFetcher` (per-posting descriptions).
3. Add its URL shapes to `ParseBoardURL` so users can add boards by pasting a link.
4. Register it in `scrape.Default()`.
5. Add a fixture-driven test with `httptest` (see `providers_test.go`). Only public feeds; see [ADR 0004](adr/0004-polite-scraping-of-public-feeds.md).

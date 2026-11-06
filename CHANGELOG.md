# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.1] - 2026-11-06

### Fixed
- `jobscout mcp` now stops promptly on Ctrl+C or `kill` even when no assistant is sending messages. Before, it ignored
  the stop request until the next message arrived and had to be force-killed. A requested stop no longer prints an
  error. (AI assistants such as Claude were not affected, because they close the connection when they exit.)

### Changed
- Refreshed the dashboard screenshot in the documentation (taken after a real sync).
- Updated the GitHub Actions used to build and release jobscout (`checkout`, `setup-go`, `golangci-lint-action`,
  `goreleaser-action`). No change to the program itself.

## [0.1.0] - 2026-11-06

First public release.

### Highlights
- **Double-click to start.** Download, unzip, double-click: the dashboard opens in your browser, fetches openings from
  about 150 companies on first run, and keeps itself fresh every 6 hours while it's open.
- **Six hiring platforms:** Greenhouse, Lever, Ashby, Workable, Recruitee and SmartRecruiters. Add any company by
  pasting one of its job links.
- **Knows what's new and what's gone.** Every job keeps its first-seen and last-seen dates; jobs taken down are marked
  closed (never deleted) and reopened if they come back.
- **Powerful search:** `OR`, `AND`, `NOT`, `-word`, `"exact phrases"`, prefixes and field filters
  (`company:`, `title:`, `loc:`, `desc:` …), in titles only or the full description.
- **Saved searches** with a "new since you last looked" badge.
- **Track applications:** statuses (saved, applied, interviewing, offer, rejected), tags and dated notes.
- **Group locations:** one row per role instead of one per city.
- **Export** every match to CSV for Excel or Google Sheets.
- **AI assistants:** an MCP server lets Claude and other assistants search your jobs; read-only unless you allow
  writes, and every change is recorded.
- **Private and local:** a single file on your computer; the dashboard is reachable only from your own machine.

### Included
- Web dashboard, command line (`init`, `scan`, `search`, `serve`, `board`, `saved`, `import`, `maintenance`,
  `backup`, `stats`), local REST API (`api/openapi.yaml`) and MCP server.
- SQLite datastore (WAL, FTS5, compressed descriptions) with an append-only change feed and audit log.
- Polite scraping: identified User-Agent, per-platform concurrency limits, retries with backoff, conditional requests;
  companies that return "not found" five times in a row are paused automatically.
- `jobscout import` to load snapshots from the earlier Python scripts, including first-seen history.
- Builds for macOS (Apple silicon and Intel), Windows and Linux (amd64, arm64).
- Documentation for non-technical users (User Guide, FAQ) and developers (architecture, ADRs, API), with diagrams.

### Known limitations
- Builds are not code-signed yet, so macOS and Windows ask for confirmation on first launch
  ([how to allow it](docs/user-guide.md#if-your-computer-blocks-it)).
- Workday, iCIMS, Taleo and other platforms without public job feeds are not supported.
- SmartRecruiters descriptions are fetched gradually (up to 50 new jobs per company per sync).
- Ashby doesn't publish company names, so those companies show a readable form of their web address until renamed.

[Unreleased]: https://github.com/manikanta-varaganti/jobscout/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/manikanta-varaganti/jobscout/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/manikanta-varaganti/jobscout/releases/tag/v0.1.0

# Contributing

## Setup
Go 1.27+ and `make`. No C compiler is needed.

```sh
make check      # gofmt check, go vet, tests with -race (what CI runs)
make lint       # golangci-lint
make run        # build and start the dashboard
```

## Guidelines
- Keep layers one-directional (see docs/architecture.md). Transports contain no business logic.
- Tests never use the live network; use `httptest` fixtures. Store tests use a real SQLite file in `t.TempDir()`.
- Schema changes are new numbered files in `internal/store/migrations`; never edit a shipped one.
- Never commit data: `*.db`, `*.jsonl` and `.env` are git-ignored.
- Commit messages: imperative mood, e.g. "Add Workable provider". Update `CHANGELOG.md` for user-visible changes.
- New providers must use only public feeds and respect the politeness rules in ADR 0004.

## Documentation
The docs have three audiences. Put new material where its reader will look:

| Reader | Where |
|---|---|
| Someone who just wants to find jobs (no technical background) | `README.md` (top half), `docs/user-guide.md`, `docs/faq.md` |
| Someone connecting an AI assistant | `docs/mcp.md` (plain-language part first, reference last) |
| Contributors | `docs/architecture.md`, `docs/adr/`, `api/openapi.yaml`, the collapsed section of the README |

Guidelines:
- **Explain flows with a diagram.** Use [Mermaid](https://mermaid.js.org) in a ```` ```mermaid ```` block; GitHub renders it. Preview at <https://mermaid.live>.
  Avoid `;`, `<` and raw `"` inside node labels (use `#quot;`); put entity attributes on separate lines in `erDiagram`.
- **Plain words for user docs.** Say "company" and "sync" rather than "board" and "scan" in user-facing text; define any term you must use in the user guide's glossary.
- **Show, then tell.** A screenshot or diagram, then numbered steps that each start with a verb.
- If the UI changes, update `docs/images/` (a 1400×1050 browser screenshot) and the dashboard tour table in the user guide.
- `make docs-check` verifies every relative link and anchor; CI runs it too.

## Pull requests
Small and focused, with tests, passing CI on macOS, Windows and Linux.

// Package cli implements the jobscout command line. Commands are thin: they parse flags and call the service.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/manikanta-varaganti/jobscout/internal/config"
	"github.com/manikanta-varaganti/jobscout/internal/scrape"
	"github.com/manikanta-varaganti/jobscout/internal/service"
	"github.com/manikanta-varaganti/jobscout/internal/store"
)

type env struct {
	ctx         context.Context
	interactive bool // stdin is a terminal
	in          io.Reader
	out         io.Writer
	err         io.Writer
	version     string
	cfg         config.Config
	log         *slog.Logger
}

type command struct {
	name, args, summary string
	run                 func(e *env, args []string) error
}

func commands() []command {
	return []command{
		{"init", "", "create the database and load the starter board list", cmdInit},
		{"scan", "", "fetch every board now", cmdScan},
		{"search", "[QUERY] [flags]", "search jobs from the terminal", cmdSearch},
		{"serve", "[flags]", "run the web dashboard and API (optionally with scheduled scans)", cmdServe},
		{"mcp", "[--allow-write]", "run the MCP server on stdio for AI agents", cmdMCP},
		{"board", "list|add|add-url|enable|disable|rename", "manage job boards", cmdBoard},
		{"saved", "list|add|rm", "manage saved searches", cmdSaved},
		{"import", "FILE...", "import legacy ats-inventory-*.jsonl snapshots", cmdImport},
		{"stats", "", "show datastore counts", cmdStats},
		{"maintenance", "[flags]", "compress old text, prune closed jobs, vacuum", cmdMaintenance},
		{"backup", "DEST.db", "write a consistent copy of the database", cmdBackup},
		{"version", "", "print the version", func(e *env, _ []string) error { _, err := fmt.Fprintln(e.out, e.version); return err }},
	}
}

// Run executes the CLI and returns the process exit code (0 ok, 1 error, 2 usage).
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, version string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	level := slog.LevelInfo
	if os.Getenv("JOBSCOUT_DEBUG") != "" {
		level = slog.LevelDebug
	}
	// Logs always go to stderr: the MCP transport owns stdout.
	e := &env{ctx: ctx, interactive: isInteractive(stdin), in: stdin, out: stdout, err: stderr, version: version, cfg: cfg,
		log: slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))}

	// No arguments is what double-clicking the program does: start the dashboard.
	if len(args) == 0 {
		return launch(e)
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(stdout)
		return 0
	}
	for _, c := range commands() {
		if c.name == args[0] {
			err := c.run(e, args[1:])
			switch {
			case err == nil:
				return 0
			case err == flag.ErrHelp:
				return 0
			case isUsage(err):
				fmt.Fprintln(stderr, err)
				return 2
			default:
				fmt.Fprintln(stderr, "error:", err)
				return 1
			}
		}
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
	usage(stderr)
	return 2
}

type usageError string

func (u usageError) Error() string { return "usage: jobscout " + string(u) }

func isUsage(err error) bool { _, ok := err.(usageError); return ok }

func usage(w io.Writer) {
	fmt.Fprintln(w, "jobscout: local job-postings inventory\n\nUsage: jobscout <command> [arguments]\n       jobscout            (no command: start the dashboard; same as double-clicking it)\n\nCommands:")
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-12s %-40s %s\n", c.name, c.args, c.summary)
	}
	fmt.Fprintln(w, "\nEnvironment: JOBSCOUT_HOME, JOBSCOUT_DB, JOBSCOUT_ADDR, JOBSCOUT_USER_AGENT, JOBSCOUT_WORKERS, JOBSCOUT_PER_HOST, JOBSCOUT_DEBUG")
	fmt.Fprintln(w, "Run `jobscout <command> -h` for command flags.")
}

// open opens the datastore and builds the service. close stops background work, then the store.
func (e *env) open() (svc *service.Service, closeFn func(), err error) {
	st, err := store.Open(e.cfg.DBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", e.cfg.DBPath, err)
	}
	svc = service.New(st, scrape.NewClient(e.cfg.UserAgent, e.cfg.PerHost), e.cfg.Workers, e.log)
	return svc, func() {
		svc.Close()
		if err := st.Close(); err != nil {
			e.log.Error("close database", "err", err)
		}
	}, nil
}

func trunc(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

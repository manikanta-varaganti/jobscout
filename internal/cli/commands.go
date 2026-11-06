package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/manikanta-varaganti/jobscout/internal/importer"
	"github.com/manikanta-varaganti/jobscout/internal/query"
	"github.com/manikanta-varaganti/jobscout/internal/service"
	"github.com/manikanta-varaganti/jobscout/internal/store"
	"github.com/manikanta-varaganti/jobscout/internal/transport/httpapi"
	"github.com/manikanta-varaganti/jobscout/internal/transport/mcp"
	"github.com/manikanta-varaganti/jobscout/web"
)

func newFlags(e *env, name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(e.err)
	return fs
}

// parseMixed parses flags that may appear before or after positional arguments
// (the standard library stops at the first positional) and returns the positionals.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func orDash(s string) string {
	if s == "" {
		return "never"
	}
	return s
}

func cmdInit(e *env, _ []string) error {
	svc, done, err := e.open()
	if err != nil {
		return err
	}
	defer done()
	n, err := svc.SeedBoards(e.ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.out, "database: %s\nadded %d starter boards\n", e.cfg.DBPath, n)
	return err
}

func cmdScan(e *env, args []string) error {
	if err := newFlags(e, "scan").Parse(args); err != nil {
		return err
	}
	svc, done, err := e.open()
	if err != nil {
		return err
	}
	defer done()
	if boards, err := svc.Store.ListBoards(e.ctx, true); err != nil {
		return err
	} else if len(boards) == 0 {
		if _, err := svc.SeedBoards(e.ctx); err != nil {
			return err
		}
	}
	sum, err := svc.Scan(e.ctx)
	for _, f := range sum.Failures {
		fmt.Fprintf(e.err, "FAIL %s:%s [%s] %s\n", f.ATS, f.Slug, f.Kind, f.Error)
	}
	if sum.Boards > 0 {
		fmt.Fprintf(e.out, "boards=%d ok=%d failed=%d seen=%d added=%d updated=%d closed=%d enriched=%d in %s\n",
			sum.Boards, sum.OK, sum.Failed, sum.Seen, sum.Added, sum.Updated, sum.Closed, sum.Enriched, sum.Duration.Round(100*time.Millisecond))
	}
	return err
}

// searchFlags registers the shared search flags on fs.
func searchFlags(fs *flag.FlagSet) func(q string) store.SearchParams {
	scope := fs.String("scope", "title", "where plain words match: title | tcl | all")
	ats := fs.String("ats", "", "only this ATS")
	company := fs.String("company", "", "only this board slug")
	target := fs.Bool("target", false, "only software/backend titles")
	newToday := fs.Bool("new", false, "only first seen today")
	days := fs.Int("posted-days", 0, "published in the last N days")
	group := fs.Bool("group", false, "one row per company+title")
	sort := fs.String("sort", "", "published | first_seen | company | title")
	return func(q string) store.SearchParams {
		return store.SearchParams{Q: q, Scope: query.Scope(*scope), ATS: *ats, Company: *company, TargetOnly: *target,
			NewToday: *newToday, PostedDays: *days, Group: *group, Sort: *sort}
	}
}

func cmdSearch(e *env, args []string) error {
	fs := newFlags(e, "search")
	q := fs.String("q", "", `query, e.g. 'software OR "full stack"'`)
	limit := fs.Int("limit", 25, "max rows")
	asJSON := fs.Bool("json", false, "print JSON")
	params := searchFlags(fs)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if *q == "" {
		*q = strings.Join(pos, " ")
	}
	svc, done, err := e.open()
	if err != nil {
		return err
	}
	defer done()
	p := params(*q)
	p.Limit = *limit
	res, err := svc.Store.Search(e.ctx, p)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(e.out)
		enc.SetIndent("", " ")
		return enc.Encode(res)
	}
	tw := tabwriter.NewWriter(e.out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tCOMPANY\tTITLE\tLOCATION\tPOSTED")
	for _, j := range res.Jobs {
		loc := j.Location
		if j.Postings > 1 {
			loc = fmt.Sprintf("%d locations", len(j.Locations))
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", j.ID, trunc(j.CompanyName, 20), trunc(j.Title, 52), trunc(loc, 24), strings.SplitN(j.PublishedAt, "T", 2)[0])
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.out, "\n%d of %d matching openings\n", len(res.Jobs), res.Total)
	return err
}

func cmdStats(e *env, _ []string) error {
	svc, done, err := e.open()
	if err != nil {
		return err
	}
	defer done()
	s, err := svc.Store.Stats(e.ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.out, "database      %s\njobs          %d (open %d, software/backend %d)\nnew today     %d\nboards        %d (%d disabled)\nlast scan     %s\nlast event id %d\nschema        v%d\n",
		e.cfg.DBPath, s.TotalJobs, s.OpenJobs, s.TargetJobs, s.NewToday, s.Boards, s.DisabledBoards, orDash(s.LastScan), s.LastEventID, s.SchemaVersion)
	return err
}

func cmdBoard(e *env, args []string) error {
	const u = usageError("board list [--all] | add ATS SLUG | add-url URL | enable ATS SLUG | disable ATS SLUG | rename ATS SLUG NAME")
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	svc, done, err := e.open()
	if err != nil {
		return err
	}
	defer done()
	ctx := e.ctx
	switch {
	case sub == "list":
		fs := newFlags(e, "board list")
		all := fs.Bool("all", false, "include disabled boards")
		if err := fs.Parse(args); err != nil {
			return err
		}
		bs, err := svc.Store.ListBoards(ctx, *all)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(e.out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ATS\tBOARD\tNAME\tSTATUS\tJOBS\tLAST SCAN")
		for _, b := range bs {
			status := orDash(b.LastStatus)
			if b.Disabled {
				status = "disabled"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", b.ATS, b.Slug, trunc(b.Name(), 28), status, b.JobCount, b.LastScannedAt)
		}
		return tw.Flush()
	case sub == "add" && len(args) == 2:
		added, err := svc.AddBoard(ctx, strings.ToLower(args[0]), args[1])
		return report(e.out, added, err, args[0]+"/"+args[1])
	case sub == "add-url" && len(args) == 1:
		ats, slug, added, err := svc.AddBoardURL(ctx, args[0])
		return report(e.out, added, err, ats+"/"+slug)
	case (sub == "enable" || sub == "disable") && len(args) == 2:
		if err := svc.Store.SetBoardEnabled(ctx, args[0], args[1], sub == "enable"); err != nil {
			return err
		}
		_, err := fmt.Fprintln(e.out, sub+"d", args[0]+"/"+args[1])
		return err
	case sub == "rename" && len(args) == 3:
		if err := svc.Store.SetDisplayName(ctx, args[0], args[1], args[2]); err != nil {
			return err
		}
		_, err := fmt.Fprintf(e.out, "renamed %s/%s to %q\n", args[0], args[1], args[2])
		return err
	}
	return u
}

func report(w io.Writer, added bool, err error, what string) error {
	if err != nil {
		return err
	}
	msg := "added " + what
	if !added {
		msg = what + " is already tracked"
	}
	_, err = fmt.Fprintln(w, msg)
	return err
}

func cmdSaved(e *env, args []string) error {
	const u = usageError(`saved list | add NAME [QUERY] [search flags] | rm ID`)
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	svc, done, err := e.open()
	if err != nil {
		return err
	}
	defer done()
	switch {
	case sub == "list":
		list, err := svc.Store.ListSavedSearches(e.ctx, true)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(e.out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tTOTAL\tNEW\tQUERY")
		for _, s := range list {
			fmt.Fprintf(tw, "%d\t%s\t%d\t%d\t%s\n", s.ID, s.Name, s.Total, s.NewCount, trunc(s.Params.Q, 50))
		}
		return tw.Flush()
	case sub == "add":
		fs := newFlags(e, "saved add")
		params := searchFlags(fs)
		pos, err := parseMixed(fs, args)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return u
		}
		id, err := svc.Store.SaveSearch(e.ctx, pos[0], params(strings.Join(pos[1:], " ")))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(e.out, "saved %q (id %d)\n", pos[0], id)
		return err
	case sub == "rm" && len(args) == 1:
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return u
		}
		if err := svc.Store.DeleteSavedSearch(e.ctx, id); err != nil {
			return err
		}
		_, err = fmt.Fprintln(e.out, "deleted", id)
		return err
	}
	return u
}

func cmdImport(e *env, args []string) error {
	if len(args) == 0 {
		return usageError("import FILE.jsonl [FILE.jsonl ...]  (globs like 'ats-inventory-*.jsonl' are expanded)")
	}
	var paths []string
	for _, a := range args {
		m, err := filepath.Glob(a)
		if err != nil {
			return err
		}
		if len(m) == 0 {
			m = []string{a}
		}
		paths = append(paths, m...)
	}
	svc, done, err := e.open()
	if err != nil {
		return err
	}
	defer done()
	sum, err := importer.ImportJSONL(e.ctx, svc.Store, paths, func(f string, a ...any) { fmt.Fprintf(e.out, f+"\n", a...) })
	fmt.Fprintf(e.out, "files=%d records=%d skipped=%d new jobs=%d\n", sum.Files, sum.Records, sum.Skipped, sum.Added)
	return err
}

// parseDays accepts Go durations plus a "d" suffix for days (e.g. 90d).
func parseDays(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func cmdMaintenance(e *env, args []string) error {
	fs := newFlags(e, "maintenance")
	prune := fs.String("prune-closed", "", "drop descriptions of jobs closed longer than this (e.g. 90d); rows and history are kept")
	vacuum := fs.Bool("vacuum", false, "rebuild the file to return free space to the OS (needs free disk equal to the DB size)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, err := parseDays(*prune)
	if err != nil {
		return err
	}
	svc, done, err := e.open()
	if err != nil {
		return err
	}
	defer done()
	rep, err := svc.Store.Maintain(e.ctx, d, *vacuum)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.out, "compressed %d descriptions, pruned %d; size %.1f MB -> %.1f MB\n",
		rep.Compressed, rep.Pruned, float64(rep.BytesBefore)/1e6, float64(rep.BytesAfter)/1e6)
	return err
}

func cmdBackup(e *env, args []string) error {
	if len(args) != 1 {
		return usageError("backup DEST.db")
	}
	svc, done, err := e.open()
	if err != nil {
		return err
	}
	defer done()
	if err := svc.Store.Backup(e.ctx, args[0]); err != nil {
		return err
	}
	_, err = fmt.Fprintln(e.out, "wrote", args[0])
	return err
}

func cmdMCP(e *env, args []string) error {
	fs := newFlags(e, "mcp")
	allow := fs.Bool("allow-write", false, "enable tools that modify data (status, tags, notes, saved searches, boards, run_scan)")
	actor := fs.String("actor", "mcp:agent", "name recorded in the audit log for writes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, done, err := e.open()
	if err != nil {
		return err
	}
	defer done()
	srv := &mcp.Server{Svc: svc, AllowWrite: *allow, Actor: *actor, Version: e.version, Log: e.log}
	return srv.Serve(e.ctx, e.in, e.out)
}

func cmdServe(e *env, args []string) error {
	fs := newFlags(e, "serve")
	addr := fs.String("addr", "127.0.0.1:8765", "listen address (keep it on loopback)")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	every := fs.Duration("scan-every", 0, "run a scan on this interval, e.g. 6h (0 = only when you click Sync)")
	syncIfOlder := fs.Duration("sync-if-older", 0, "at start-up, sync if the last sync is older than this, or never happened (0 = off)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, done, err := e.open()
	if err != nil {
		return err
	}
	defer done() // cancels and waits for a running scan before the database closes
	if boards, err := svc.Store.ListBoards(e.ctx, true); err == nil && len(boards) == 0 {
		if n, err := svc.SeedBoards(e.ctx); err == nil {
			e.log.Info("loaded starter boards", "count", n)
		}
	}
	if *syncIfOlder > 0 {
		st, err := svc.Store.Stats(e.ctx)
		if err == nil && syncDue(st.LastScan, *syncIfOlder, time.Now()) {
			if err := svc.StartScan(e.ctx); err == nil {
				fmt.Fprintln(e.out, "Fetching the latest jobs in the background (about a minute)…")
			}
		}
	}
	if *every > 0 {
		go func() {
			t := time.NewTicker(*every)
			defer t.Stop()
			for {
				select {
				case <-e.ctx.Done():
					return
				case <-t.C:
					if err := svc.StartScan(e.ctx); err == nil {
						e.log.Info("scheduled scan started")
					} else if !errors.Is(err, service.ErrScanRunning) {
						e.log.Error("scheduled scan", "err", err)
					}
				}
			}
		}()
	}
	url := "http://" + *addr + "/"
	fmt.Fprintf(e.out, "jobscout %s serving %s  (Ctrl+C to stop)\n", e.version, url)
	if !*noOpen {
		go openBrowser(url)
	}
	return httpapi.New(svc, web.UI(), e.log).ListenAndServe(e.ctx, *addr)
}

// syncDue reports whether a start-up sync is needed: never synced, or the last sync is older than maxAge.
func syncDue(lastScan string, maxAge time.Duration, now time.Time) bool {
	if lastScan == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, lastScan)
	return err != nil || now.Sub(t) >= maxAge
}

// openBrowser is a variable so tests can stub it.
var openBrowser = func(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start() // best effort; the URL is printed anyway
}

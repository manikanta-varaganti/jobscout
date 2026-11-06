package store

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manikanta-varaganti/jobscout/internal/query"
)

var ctx = context.Background()

func open(t testing.TB) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "x", "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func job(id, title string) Job {
	return Job{ExtID: id, Title: title, Location: "Remote", URL: "https://x/" + id, Description: "build distributed systems", PublishedAt: "2026-11-01T10:00:00Z"}
}

func apply(t testing.TB, s *Store, ats, board string, jobs []Job, at string, complete bool) ApplyStats {
	t.Helper()
	st, err := s.ApplyBoard(ctx, ats, board, jobs, at, complete)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func search(t testing.TB, s *Store, p SearchParams) SearchResult {
	t.Helper()
	r, err := s.Search(ctx, p)
	if err != nil {
		t.Fatalf("search %+v: %v", p, err)
	}
	return r
}

func count(t testing.TB, s *Store, p SearchParams) int { return search(t, s, p).Total }

func idOf(t *testing.T, s *Store, ext string) int64 {
	t.Helper()
	var id int64
	if err := s.r.QueryRow(`SELECT id FROM jobs WHERE ext_id=?`, ext).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUpsertCloseReopenAndEvents(t *testing.T) {
	s := open(t)
	t1 := "2026-11-01T00:00:00Z"
	if st := apply(t, s, "lever", "acme", []Job{job("1", "Backend Engineer"), job("2", "Sales Lead"), job("2", "Sales Lead")}, t1, true); st.Added != 2 {
		t.Fatalf("%+v (duplicate ids in one feed must be ignored)", st)
	}
	j1 := job("1", "Backend Engineer II")
	if st := apply(t, s, "lever", "acme", []Job{j1}, t1, true); st.Closed != 1 || st.Updated != 1 {
		t.Fatalf("same-second rescan must still close: %+v", st)
	}
	if n := count(t, s, SearchParams{}); n != 1 {
		t.Fatalf("open = %d", n)
	}
	if st := apply(t, s, "lever", "acme", []Job{j1, job("2", "Sales Lead")}, "2026-11-03T00:00:00Z", true); st.Reopened != 1 {
		t.Fatalf("%+v", st)
	}
	j, err := s.GetJob(ctx, idOf(t, s, "1"))
	if err != nil || j.FirstSeen != t1 || j.Description != "build distributed systems" || j.CompanyName != "Acme" {
		t.Fatalf("%+v %v", j, err)
	}
	ev, err := s.EventsSince(ctx, 0, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, e := range ev {
		kinds[e.Kind]++
	}
	if kinds["added"] != 2 || kinds["closed"] != 1 || kinds["reopened"] != 1 || kinds["updated"] != 1 {
		t.Errorf("events %v", kinds)
	}
	if count(t, s, SearchParams{Q: `"engineer ii"`}) != 1 {
		t.Error("updated title not indexed")
	}
}

func TestIncompleteScanDoesNotClose(t *testing.T) {
	s := open(t)
	apply(t, s, "ashby", "b", []Job{job("1", "Software Engineer")}, "2026-11-01T00:00:00Z", true)
	if st := apply(t, s, "ashby", "b", nil, "2026-11-02T00:00:00Z", false); st.Closed != 0 {
		t.Error("incomplete scan closed jobs")
	}
}

func TestBooleanSearch(t *testing.T) {
	s := open(t)
	apply(t, s, "greenhouse", "Acme", []Job{
		job("1", "Software Engineer"), job("2", "Senior Software Engineer"), job("3", "Full Stack Engineer"),
		job("4", "AI Researcher"), job("5", "Agentic Systems Lead"), job("6", "Member of Technical Staff"),
		job("7", "Forward Deployed Engineer"), job("8", "Email Marketing Manager"), job("9", "Support Agent"),
	}, "2026-11-01T00:00:00Z", true)
	for _, c := range []struct {
		q    string
		want int
	}{
		{"", 9}, {"software", 2}, {`"full stack"`, 1}, {"AI", 1}, {"agent", 2}, {"agent*", 2},
		{`software OR "full stack" OR AI OR agent OR "member of technical staff" OR "forward deployed engineer"`, 8},
		{"software -senior", 1}, {"software AND NOT senior", 1}, {`(software OR "full stack") AND NOT senior`, 2},
		{"company:acme software", 2}, {"ats:greenhouse AI", 1}, {"ats:lever AI", 0}, {"desc:distributed software", 2},
		{"(unbalanced OR software", 2}, {"OR", 9}, {`"`, 9}, {"c++", 0}, {"0\x00", 0},
	} {
		if got := count(t, s, SearchParams{Q: c.q}); got != c.want {
			t.Errorf("query %q: got %d want %d", c.q, got, c.want)
		}
	}
	if n := count(t, s, SearchParams{Q: "distributed", Scope: query.ScopeTitle}); n != 0 {
		t.Errorf("title scope matched description: %d", n)
	}
	if n := count(t, s, SearchParams{Q: "distributed", Scope: query.ScopeAll}); n != 9 {
		t.Errorf("all scope = %d", n)
	}
}

func TestFiltersFacetsCursorAndGrouping(t *testing.T) {
	s := open(t)
	now := time.Now().UTC().Format(timeFmt)
	old := job("1", "Software Engineer")
	old.PublishedAt = "2020-01-01T00:00:00Z"
	fresh := job("2", "Backend Engineer")
	fresh.PublishedAt, fresh.Workplace = now, "remote"
	dupA, dupB := job("3", "Backend  Engineer"), job("4", "backend engineer")
	dupA.Location, dupB.Location = "NYC", "SF"
	dupA.PublishedAt, dupB.PublishedAt = old.PublishedAt, old.PublishedAt
	apply(t, s, "lever", "a", []Job{old, fresh, dupA, dupB}, now, true)

	if n := count(t, s, SearchParams{PostedDays: 7}); n != 1 {
		t.Errorf("posted filter %d", n)
	}
	if n := count(t, s, SearchParams{NewToday: true}); n != 4 {
		t.Errorf("new today %d", n)
	}
	if n := count(t, s, SearchParams{Workplace: "REMOTE"}); n != 1 {
		t.Errorf("workplace %d", n)
	}
	if n := count(t, s, SearchParams{Location: "remo%"}); n != 0 {
		t.Errorf("LIKE wildcard must be escaped: %d", n)
	}
	r := search(t, s, SearchParams{})
	if len(r.PerDay) != 30 || len(r.TopCompanies) != 1 || r.TopCompanies[0].Label != "A" || r.Jobs[0].Title != "Backend Engineer" {
		t.Errorf("facets/sort: %+v %+v", r.TopCompanies, r.Jobs[0])
	}
	// cursor pagination walks every row exactly once
	seen := map[int64]bool{}
	p := SearchParams{Limit: 1}
	for i := 0; i < 10; i++ {
		page := search(t, s, p)
		for _, j := range page.Jobs {
			seen[j.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		p.Cursor = page.NextCursor
	}
	if len(seen) != 4 {
		t.Errorf("cursor walk saw %d", len(seen))
	}
	if _, err := s.Search(ctx, SearchParams{Cursor: "garbage!"}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("bad cursor: %v", err)
	}
	g := search(t, s, SearchParams{Group: true, Q: "backend"})
	if g.Total != 3 || g.Groups != 1 || g.Jobs[0].Postings != 3 || len(g.Jobs[0].Locations) != 3 {
		t.Errorf("grouping: %+v", g)
	}
	if all := search(t, s, SearchParams{All: true, Limit: 1}); len(all.Jobs) != 4 || all.NextCursor != "" {
		t.Errorf("All must ignore the page limit: %d", len(all.Jobs))
	}
}

func TestAgentActionsIdempotencyAndAudit(t *testing.T) {
	s := open(t)
	apply(t, s, "lever", "a", []Job{job("1", "Backend Engineer")}, "2026-11-01T00:00:00Z", true)
	id := idOf(t, s, "1")
	w := Write{Actor: "mcp:claude"}
	if err := s.SetStatus(ctx, id, "applied", w); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTag(ctx, id, " Dream ", w); err != nil {
		t.Fatal(err)
	}
	keyed := Write{Actor: "mcp:claude", RequestKey: "req-1"}
	for i := 0; i < 3; i++ { // a retried call must apply once
		if err := s.AddNote(ctx, id, "referred by Sam", keyed); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetStatus(ctx, 999, "x", w); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing job: %v", err)
	}
	if err := s.AddTag(ctx, id, "", w); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty tag: %v", err)
	}
	j, err := s.GetJob(ctx, id)
	if err != nil || j.Status != "applied" || len(j.Tags) != 1 || j.Tags[0] != "dream" || len(j.Notes) != 1 {
		t.Errorf("%+v %v", j, err)
	}
	ev, _ := s.EventsSince(ctx, 0, []string{"status", "tag", "note"}, 10)
	if len(ev) != 3 || ev[0].Actor != "mcp:claude" {
		t.Errorf("audit %+v", ev)
	}
}

func TestBoardsDisplayNameAndAutoDisable(t *testing.T) {
	s := open(t)
	if _, err := s.AddBoard(ctx, "lever", "andurilindustries"); err != nil {
		t.Fatal(err)
	}
	apply(t, s, "lever", "andurilindustries", []Job{job("1", "Software Engineer")}, "2026-11-01T00:00:00Z", true)
	if count(t, s, SearchParams{Q: "company:industries"}) != 0 {
		t.Fatal("precondition: name not yet known")
	}
	if err := s.SetDisplayName(ctx, "lever", "andurilindustries", "Anduril Industries"); err != nil {
		t.Fatal(err)
	}
	if count(t, s, SearchParams{Q: "company:industries"}) != 1 {
		t.Error("display name not searchable after rename")
	}
	if err := s.SetDisplayNameIfEmpty(ctx, "lever", "andurilindustries", "Other"); err != nil {
		t.Fatal(err)
	}
	if r := search(t, s, SearchParams{}); r.Jobs[0].CompanyName != "Anduril Industries" {
		t.Errorf("feed name overrode user name: %s", r.Jobs[0].CompanyName)
	}
	for i := 0; i < DisableAfterNotFound; i++ {
		if err := s.SetBoardResult(ctx, "lever", "andurilindustries", "not_found", "404", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := s.ListBoards(ctx, false); len(b) != 0 {
		t.Error("board should be disabled after repeated 404s")
	}
	if err := s.SetBoardEnabled(ctx, "lever", "andurilindustries", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.ListBoards(ctx, false); len(b) != 1 || b[0].ConsecutiveFailures != 0 {
		t.Errorf("re-enable: %+v", b)
	}
	if err := s.SetBoardEnabled(ctx, "lever", "nope", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown board: %v", err)
	}
	if Humanize("ScribdInc") != "Scribd Inc" || Humanize("flow-engineering") != "Flow Engineering" {
		t.Error("humanize")
	}
}

func TestSavedSearches(t *testing.T) {
	s := open(t)
	apply(t, s, "lever", "a", []Job{job("1", "Backend Engineer"), job("2", "Sales Lead")}, Now(), true)
	list, err := s.ListSavedSearches(ctx, true)
	if err != nil || len(list) != 1 || list[0].Name != "Software & backend" || list[0].Total != 1 {
		t.Fatalf("default saved search: %+v %v", list, err)
	}
	id, err := s.SaveSearch(ctx, "Sales", SearchParams{Q: "sales", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSearch(ctx, "sales", SearchParams{Q: "lead"}); err != nil { // same name, case-insensitive: replaces
		t.Fatal(err)
	}
	ss, err := s.GetSavedSearch(ctx, id)
	if err != nil || ss.Params.Q != "lead" || ss.Total != 1 {
		t.Fatalf("%+v %v", ss, err)
	}
	if err := s.MarkSavedSearchViewed(ctx, id); err != nil {
		t.Fatal(err)
	}
	if ss, _ = s.GetSavedSearch(ctx, id); ss.NewCount != 0 {
		t.Errorf("new after viewing = %d", ss.NewCount)
	}
	if err := s.DeleteSavedSearch(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSearch(ctx, " ", SearchParams{}); !errors.Is(err, ErrInvalid) {
		t.Error("blank name accepted")
	}
}

func TestLocks(t *testing.T) {
	s := open(t)
	ok, _, err := s.AcquireLock(ctx, "scan", "p1", time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, h, _ := s.AcquireLock(ctx, "scan", "p2", time.Minute); ok || h.Owner != "p1" {
		t.Errorf("second owner got the lock: %v %+v", ok, h)
	}
	if ok, _, _ := s.AcquireLock(ctx, "scan", "p1", time.Minute); !ok {
		t.Error("owner could not renew")
	}
	if err := s.ReleaseLock(ctx, "scan", "p1"); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := s.AcquireLock(ctx, "scan", "p2", -time.Second); !ok {
		t.Error("released lock not available")
	}
	if ok, _, _ := s.AcquireLock(ctx, "scan", "p3", time.Minute); !ok {
		t.Error("expired lease must be reclaimable")
	}
}

func TestMaintenanceCompressAndPrune(t *testing.T) {
	s := open(t)
	apply(t, s, "lever", "a", []Job{job("1", "Backend Engineer"), job("2", "Sales")}, "2020-01-01T00:00:00Z", true)
	id := idOf(t, s, "1")
	// simulate a row written before compression existed
	if _, err := s.w.Exec(`UPDATE job_text SET description='legacy text', description_z=NULL WHERE job_id=?`, id); err != nil {
		t.Fatal(err)
	}
	apply(t, s, "lever", "a", []Job{job("1", "Backend Engineer")}, "2020-01-02T00:00:00Z", true) // closes job 2 long ago
	rep, err := s.Maintain(ctx, 24*time.Hour, true)
	if err != nil || rep.Compressed != 1 || rep.Pruned != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if j, _ := s.GetJob(ctx, id); j.Description != "legacy text" {
		t.Errorf("compressed text not readable: %q", j.Description)
	}
	if j, _ := s.GetJob(ctx, idOf(t, s, "2")); j.Description != "" || j.Title != "Sales" {
		t.Errorf("prune must keep the row but drop text: %+v", j)
	}
}

func TestDescriptionEnrichment(t *testing.T) {
	s := open(t)
	j := job("1", "Engineer")
	j.Description = ""
	apply(t, s, "smartrecruiters", "acme", []Job{j}, "2026-11-01T00:00:00Z", true)
	missing, err := s.MissingDescriptions(ctx, "smartrecruiters", "acme", 10)
	if err != nil || len(missing) != 1 {
		t.Fatalf("%v %v", missing, err)
	}
	if err := s.SetDescription(ctx, missing["1"], "We build REST APIs in Go"); err != nil {
		t.Fatal(err)
	}
	if count(t, s, SearchParams{Q: "desc:golang OR desc:go"}) != 1 {
		t.Error("enriched description not indexed")
	}
	if m, _ := s.MissingDescriptions(ctx, "smartrecruiters", "acme", 10); len(m) != 0 {
		t.Error("still missing after enrichment")
	}
}

func TestPersistsAcrossReopenWithSpacesInPath(t *testing.T) {
	odd := "50% #1?"
	if runtime.GOOS == "windows" {
		odd = "50% #1" // '?' is not allowed in Windows file names
	}
	path := filepath.Join(t.TempDir(), "Application Support", odd, "jobs.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	apply(t, s, "lever", "a", []Job{job("1", "Backend Engineer")}, "2026-11-01T00:00:00Z", true)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if count(t, s2, SearchParams{Q: "backend"}) != 1 {
		t.Error("data or FTS lost after reopen")
	}
	if v, _ := s2.SchemaVersion(ctx); v != 2 {
		t.Errorf("schema version %d", v)
	}
}

func TestDSNWindowsForm(t *testing.T) {
	if got := DSN(`C:/Users/me/AppData/Roaming/jobscout/jobscout.db`, true); !strings.HasPrefix(got, "file:///C:/Users/") {
		t.Errorf("windows dsn: %s", got)
	}
}

func TestBackup(t *testing.T) {
	s := open(t)
	apply(t, s, "lever", "a", []Job{job("1", "Backend Engineer")}, "2026-11-01T00:00:00Z", true)
	dst := filepath.Join(t.TempDir(), "b.db")
	if err := s.Backup(ctx, dst); err != nil {
		t.Fatal(err)
	}
	b, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if count(t, b, SearchParams{Q: "backend"}) != 1 {
		t.Error("backup missing data")
	}
}

// FuzzSearch: no query string may make the search fail.
func FuzzSearch(f *testing.F) {
	s := open(f)
	apply(f, s, "lever", "a", []Job{job("1", "Software Engineer")}, "2026-01-01T00:00:00Z", true)
	for _, q := range []string{`software OR "full stack"`, `-x`, `(a OR b) AND NOT c`, `title:"x y"*`, `""`, `{`, `NOT`, `a:b:c`, `"a" NEAR b`, "0\x00", `^a`, `a*b`} {
		f.Add(q)
	}
	f.Fuzz(func(t *testing.T, q string) {
		for _, sc := range []query.Scope{query.ScopeTitle, query.ScopeAll} {
			if _, err := s.Search(ctx, SearchParams{Q: q, Scope: sc, Limit: 1}); err != nil {
				t.Fatalf("query %q scope %s: %v", q, sc, err)
			}
		}
	})
}

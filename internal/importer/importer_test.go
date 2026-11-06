package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/manikanta-varaganti/jobscout/internal/store"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestImportOrdersByTimeAndClosesVanishedJobs(t *testing.T) {
	dir := t.TempDir()
	rec := func(id, title, obs string, extra string) string {
		return `{"ats":"Lever","ats_job_id":"` + id + `","company_board":"Acme","title":"` + title + `","posting_url":"https://jobs.lever.co/acme/` + id + `","observed_at":"` + obs + `","published_at":1700000000000` + extra + `}` + "\n"
	}
	// file names sort the "wrong" way on purpose; ordering must come from observed_at
	newer := write(t, dir, "a-new.jsonl", rec("1", "Backend Engineer", "2026-11-05T10:00:00-08:00", ""))
	older := write(t, dir, "z-old.jsonl", rec("1", "Backend Engineer", "2026-10-01T10:00:00-07:00", "")+rec("2", "Sales Lead", "2026-10-01T10:00:00-07:00", "")+"garbage line\n")
	st, err := store.Open(filepath.Join(dir, "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	}()
	sum, err := ImportJSONL(context.Background(), st, []string{newer, older}, nil)
	if err != nil || sum.Records != 3 || sum.Skipped != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	r, err := st.Search(context.Background(), store.SearchParams{IncludeClosed: true, Sort: "title"})
	if err != nil || r.Total != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	be, sales := r.Jobs[0], r.Jobs[1]
	if be.FirstSeen != "2026-10-01T17:00:00Z" {
		t.Errorf("first_seen = %s", be.FirstSeen)
	}
	if sales.ClosedAt == "" {
		t.Error("job missing from the newest snapshot should be closed")
	}
	if be.PublishedAt != "2023-11-14T22:13:20Z" {
		t.Errorf("legacy epoch-ms published_at = %s", be.PublishedAt)
	}
}

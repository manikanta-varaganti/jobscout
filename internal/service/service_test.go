package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manikanta-varaganti/jobscout/internal/domain"
	"github.com/manikanta-varaganti/jobscout/internal/scrape"
	"github.com/manikanta-varaganti/jobscout/internal/store"
)

var ctx = context.Background()

type fakeProvider struct {
	jobs     map[string][]domain.Job
	errs     map[string]error
	etag     string
	name     string
	block    chan struct{} // if set, Fetch waits on it or ctx
	details  map[string]string
	detailsN int32
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Fetch(c context.Context, _ *scrape.Client, slug, prev string) (scrape.Result, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-c.Done():
			return scrape.Result{}, c.Err()
		}
	}
	if err := f.errs[slug]; err != nil {
		return scrape.Result{}, err
	}
	if f.etag != "" && prev == f.etag {
		return scrape.Result{NotModified: true, ETag: f.etag}, nil
	}
	return scrape.Result{Jobs: f.jobs[slug], ETag: f.etag, BoardName: f.name}, nil
}
func (f *fakeProvider) FetchDescription(_ context.Context, _ *scrape.Client, _, ext string) (string, error) {
	atomic.AddInt32(&f.detailsN, 1)
	return f.details[ext], nil
}

func newSvcAt(t *testing.T, path string, p *fakeProvider) *Service {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, scrape.NewClient("t", 2), 4, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Registry = scrape.Registry{"fake": p}
	t.Cleanup(func() {
		s.Close()
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func newSvc(t *testing.T, p *fakeProvider) *Service {
	return newSvcAt(t, filepath.Join(t.TempDir(), "j.db"), p)
}

func j(id, title string) domain.Job {
	return domain.Job{ExtID: id, Title: title, URL: "https://x/" + id, Description: "d"}
}

func mustAdd(t *testing.T, s *Service, slugs ...string) {
	t.Helper()
	for _, slug := range slugs {
		if _, err := s.AddBoard(ctx, "fake", slug); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanAddsClosesAndIsolatesFailures(t *testing.T) {
	p := &fakeProvider{jobs: map[string][]domain.Job{"a": {j("1", "Backend Engineer"), j("2", "Software Engineer")}, "b": {j("9", "Data Engineer")}},
		errs: map[string]error{"bad": &scrape.FetchError{Kind: scrape.KindNotFound, Msg: "404"}}}
	s := newSvc(t, p)
	mustAdd(t, s, "a", "b", "bad")
	sum, err := s.Scan(ctx)
	if err != nil || sum.OK != 2 || sum.Failed != 1 || sum.Added != 3 || sum.Failures[0].Kind != "not_found" {
		t.Fatalf("%+v %v", sum, err)
	}
	p.jobs["a"] = []domain.Job{j("1", "Backend Engineer")}
	if sum, _ = s.Scan(ctx); sum.Closed != 1 || sum.Added != 0 {
		t.Fatalf("second scan %+v", sum)
	}
	if st := s.ScanStatus(ctx); st.State != "done" || st.Done != 3 {
		t.Errorf("status %+v", st)
	}
}

func TestEmptyListingDoesNotCloseEverything(t *testing.T) {
	p := &fakeProvider{jobs: map[string][]domain.Job{"a": {j("1", "Backend Engineer")}}}
	s := newSvc(t, p)
	mustAdd(t, s, "a")
	if _, err := s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	p.jobs["a"] = nil
	if sum, _ := s.Scan(ctx); sum.Closed != 0 {
		t.Errorf("closed %d jobs on an empty listing", sum.Closed)
	}
}

func TestNotModifiedKeepsJobsOpenAndCount(t *testing.T) {
	p := &fakeProvider{jobs: map[string][]domain.Job{"a": {j("1", "Backend Engineer")}}, etag: `"e1"`}
	s := newSvc(t, p)
	mustAdd(t, s, "a")
	for i := 0; i < 2; i++ {
		if sum, err := s.Scan(ctx); err != nil || sum.Closed != 0 {
			t.Fatalf("%+v %v", sum, err)
		}
	}
	b, _ := s.Store.ListBoards(ctx, false)
	if b[0].JobCount != 1 {
		t.Errorf("304 must not reset job_count: %+v", b[0])
	}
}

func TestAllBoardsFailingIsAnError(t *testing.T) {
	s := newSvc(t, &fakeProvider{errs: map[string]error{"a": errors.New("boom")}})
	mustAdd(t, s, "a")
	if _, err := s.Scan(ctx); err == nil {
		t.Error("expected error when every board fails")
	}
}

func TestAddBoardValidationAndURL(t *testing.T) {
	s := newSvc(t, &fakeProvider{})
	if _, err := s.AddBoard(ctx, "fake", "bad slug%20"); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("invalid slug: %v", err)
	}
	if _, err := s.AddBoard(ctx, "nope", "ok"); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("unknown ATS: %v", err)
	}
	s.Registry = scrape.Default()
	ats, slug, added, err := s.AddBoardURL(ctx, "https://jobs.lever.co/palantir/abc")
	if err != nil || ats != "lever" || slug != "palantir" || !added {
		t.Errorf("%s %s %v %v", ats, slug, added, err)
	}
	if _, _, _, err := s.AddBoardURL(ctx, "https://example.com"); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("unknown url: %v", err)
	}
}

func TestScanLockSpansProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	p := &fakeProvider{jobs: map[string][]domain.Job{"a": {j("1", "Engineer")}}, block: make(chan struct{})}
	a := newSvcAt(t, path, p) // e.g. `jobscout serve`
	b := newSvcAt(t, path, p) // e.g. `jobscout mcp`, a separate process on the same file
	mustAdd(t, a, "a")
	if err := a.StartScan(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.StartScan(ctx); !errors.Is(err, ErrScanRunning) {
		t.Fatalf("second process started a concurrent scan: %v", err)
	}
	if st := b.ScanStatus(ctx); st.State != "running" || !st.Elsewhere {
		t.Errorf("other process should see the scan: %+v", st)
	}
	close(p.block)
	deadline := time.Now().Add(5 * time.Second)
	for a.ScanStatus(ctx).State == "running" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := b.StartScan(ctx); err != nil {
		t.Errorf("lock not released after scan: %v", err)
	}
}

func TestCloseCancelsRunningScan(t *testing.T) {
	p := &fakeProvider{block: make(chan struct{})} // never unblocks
	s := newSvc(t, p)
	mustAdd(t, s, "a")
	if err := s.StartScan(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop the scan")
	}
	if st := s.ScanStatus(ctx); st.State != "error" {
		t.Errorf("cancelled scan state %+v", st)
	}
	b, _ := s.Store.ListBoards(ctx, true)
	if b[0].ConsecutiveFailures != 0 || b[0].LastStatus != "" {
		t.Errorf("a cancelled scan must not count against the board: %+v", b[0])
	}
	if st, _ := s.Store.Stats(ctx); st.LastScan != "" {
		t.Errorf("an interrupted scan must not count as the last sync: %q", st.LastScan)
	}
}

func TestAllFailedScanIsNotALastSync(t *testing.T) {
	s := newSvc(t, &fakeProvider{errs: map[string]error{"a": errors.New("offline")}})
	mustAdd(t, s, "a")
	_, _ = s.Scan(ctx)
	if st, _ := s.Store.Stats(ctx); st.LastScan != "" {
		t.Errorf("a scan where every board failed must not count as the last sync: %q", st.LastScan)
	}
}

func TestEnrichmentAndFeedNames(t *testing.T) {
	jj := j("1", "Platform Engineer")
	jj.Description = ""
	p := &fakeProvider{jobs: map[string][]domain.Job{"a": {jj}}, name: "Acme Corp", details: map[string]string{"1": "Distributed systems in Go"}}
	s := newSvc(t, p)
	mustAdd(t, s, "a")
	sum, err := s.Scan(ctx)
	if err != nil || sum.Enriched != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	if _, err := s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&p.detailsN); n != 1 {
		t.Errorf("descriptions must be fetched once per job, got %d", n)
	}
	r, err := s.Store.Search(ctx, store.SearchParams{Q: "desc:distributed", Scope: "all"})
	if err != nil || r.Total != 1 || r.Jobs[0].CompanyName != "Acme Corp" {
		t.Errorf("%+v %v", r, err)
	}
}

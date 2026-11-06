package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/manikanta-varaganti/jobscout/internal/scrape"
	"github.com/manikanta-varaganti/jobscout/internal/store"
)

const (
	scanLock     = "scan"
	scanLockTTL  = 2 * time.Minute
	scanLockBeat = 30 * time.Second
)

// ErrScanRunning means a scan is already running in this or another jobscout process.
var ErrScanRunning = errors.New("a scan is already running")

// ScanProgress is a snapshot of the current or last scan.
type ScanProgress struct {
	State      string `json:"state"` // idle | running | done | error
	Done       int    `json:"done"`
	Total      int    `json:"total"`
	Failed     int    `json:"failed"`
	Added      int    `json:"added"`
	Closed     int    `json:"closed"`
	Message    string `json:"message"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Elsewhere  bool   `json:"elsewhere,omitempty"` // running in another process
}

// BoardFailure records one board that could not be scanned.
type BoardFailure struct {
	ATS   string `json:"ats"`
	Slug  string `json:"slug"`
	Kind  string `json:"kind"`
	Error string `json:"error"`
}

type ScanSummary struct {
	Boards, OK, Failed           int
	Seen, Added, Updated, Closed int
	Enriched                     int
	Failures                     []BoardFailure
	Duration                     time.Duration
}

// ScanStatus reports this process's scan, or a scan another process is running.
func (s *Service) ScanStatus(ctx context.Context) ScanProgress {
	s.mu.Lock()
	p := s.progress
	s.mu.Unlock()
	if p.State == "running" {
		return p
	}
	if l, held, err := s.Store.LockHolder(ctx, scanLock); err == nil && held && l.Owner != s.owner {
		return ScanProgress{State: "running", Elsewhere: true, Message: "A scan is running in another jobscout process"}
	}
	return p
}

func (s *Service) setProgress(f func(p *ScanProgress)) {
	s.mu.Lock()
	f(&s.progress)
	s.mu.Unlock()
}

// begin claims the local and cross-process scan slots.
func (s *Service) begin(ctx context.Context) error {
	s.mu.Lock()
	if s.progress.State == "running" {
		s.mu.Unlock()
		return ErrScanRunning
	}
	s.progress = ScanProgress{State: "running", Message: "Starting…", StartedAt: store.Now()}
	s.mu.Unlock()
	ok, _, err := s.Store.AcquireLock(ctx, scanLock, s.owner, scanLockTTL)
	if err != nil || !ok {
		s.setProgress(func(p *ScanProgress) { p.State = "idle"; p.Message = "" })
		if err != nil {
			return err
		}
		return ErrScanRunning
	}
	return nil
}

func (s *Service) end(sum ScanSummary, err error) {
	if rerr := s.Store.ReleaseLock(context.Background(), scanLock, s.owner); rerr != nil {
		s.Log.Error("release scan lock", "err", rerr)
	}
	s.setProgress(func(p *ScanProgress) {
		p.FinishedAt = store.Now()
		if err != nil {
			p.State, p.Message = "error", err.Error()
			return
		}
		p.State = "done"
		p.Message = fmt.Sprintf("%d/%d boards ok · %d new · %d closed · %s", sum.OK, sum.Boards, sum.Added, sum.Closed, sum.Duration.Round(time.Second))
	})
}

// StartScan begins a scan in the background, tied to the service lifetime (Close cancels it).
func (s *Service) StartScan(ctx context.Context) error {
	if err := s.begin(ctx); err != nil {
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		sum, err := s.runScan(s.base)
		s.end(sum, err)
	}()
	return nil
}

// Scan runs a scan synchronously (used by the CLI).
func (s *Service) Scan(ctx context.Context) (ScanSummary, error) {
	if err := s.begin(ctx); err != nil {
		return ScanSummary{}, err
	}
	sum, err := s.runScan(ctx)
	s.end(sum, err)
	return sum, err
}

func (s *Service) heartbeat(ctx context.Context) (stop func()) {
	hctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(scanLockBeat)
		defer t.Stop()
		for {
			select {
			case <-hctx.Done():
				return
			case <-t.C:
				if _, _, err := s.Store.AcquireLock(hctx, scanLock, s.owner, scanLockTTL); err != nil && hctx.Err() == nil {
					s.Log.Warn("renew scan lock", "err", err)
				}
			}
		}
	}()
	return func() { cancel(); <-done }
}

func (s *Service) runScan(ctx context.Context) (ScanSummary, error) {
	var sum ScanSummary
	start := time.Now()
	defer s.heartbeat(ctx)()
	boards, err := s.Store.ListBoards(ctx, false)
	if err != nil {
		return sum, err
	}
	if len(boards) == 0 {
		return sum, fmt.Errorf("no boards configured; run `jobscout init` or `jobscout board add`")
	}
	sum.Boards = len(boards)
	runID, err := s.Store.StartRun(ctx, len(boards))
	if err != nil {
		return sum, err
	}
	s.setProgress(func(p *ScanProgress) { p.Total = len(boards) })

	work := make(chan store.Board)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < s.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range work {
				r := s.scanBoard(ctx, b)
				if r.cancelled {
					continue
				}
				mu.Lock()
				if r.fail != nil {
					sum.Failed++
					sum.Failures = append(sum.Failures, *r.fail)
				} else {
					sum.OK++
					sum.Seen += r.st.Seen
					sum.Added += r.st.Added
					sum.Updated += r.st.Updated
					sum.Closed += r.st.Closed
					sum.Enriched += r.enriched
				}
				done, failed, added, closed := sum.OK+sum.Failed, sum.Failed, sum.Added, sum.Closed
				mu.Unlock()
				s.setProgress(func(p *ScanProgress) {
					p.Done, p.Failed, p.Added, p.Closed = done, failed, added, closed
					p.Message = fmt.Sprintf("Scanning boards… %d/%d", done, len(boards))
				})
			}
		}()
	}
feed:
	for _, b := range boards {
		select {
		case work <- b:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	sum.Duration = time.Since(start)
	if ctx.Err() != nil {
		// An interrupted run is left unfinished, so it never counts as "the last sync".
		s.Log.Info("scan cancelled", "boards_done", sum.OK+sum.Failed, "of", sum.Boards, "added", sum.Added)
		return sum, ctx.Err()
	}
	if err := s.Store.FinishRun(context.WithoutCancel(ctx), runID, store.RunTotals{OK: sum.OK, Failed: sum.Failed, Seen: sum.Seen, Added: sum.Added, Closed: sum.Closed}); err != nil {
		s.Log.Error("finish run", "err", err)
	}
	s.Log.Info("scan finished", "boards", sum.Boards, "ok", sum.OK, "failed", sum.Failed, "added", sum.Added, "closed", sum.Closed,
		"enriched", sum.Enriched, "duration", sum.Duration.Round(time.Millisecond))
	if sum.OK == 0 {
		return sum, fmt.Errorf("every board failed (%d)", sum.Failed)
	}
	return sum, nil
}

type boardResult struct {
	st        store.ApplyStats
	enriched  int
	fail      *BoardFailure
	cancelled bool // the scan was stopped; not the board's fault
}

func (s *Service) scanBoard(ctx context.Context, b store.Board) boardResult {
	start := time.Now()
	fail := func(kind, msg string) boardResult {
		if ctx.Err() != nil {
			// Stopped mid-flight (Ctrl+C, window closed): record nothing against the board.
			return boardResult{cancelled: true}
		}
		status := "error"
		if kind == string(scrape.KindNotFound) {
			status = "not_found"
		}
		if err := s.Store.SetBoardResult(context.WithoutCancel(ctx), b.ATS, b.Slug, status, msg, "", 0); err != nil {
			s.Log.Error("record board failure", "err", err)
		}
		s.Log.Warn("board failed", "ats", b.ATS, "board", b.Slug, "kind", kind, "err", msg, "dur", time.Since(start).Round(time.Millisecond))
		return boardResult{fail: &BoardFailure{ATS: b.ATS, Slug: b.Slug, Kind: kind, Error: msg}}
	}
	p, err := s.Registry.Get(b.ATS)
	if err != nil {
		return fail("config", err.Error())
	}
	seenAt := store.Now()
	res, err := p.Fetch(ctx, s.Client, b.Slug, b.ETag)
	if err != nil {
		kind := "error"
		var fe *scrape.FetchError
		if errors.As(err, &fe) {
			kind = string(fe.Kind)
		}
		return fail(kind, err.Error())
	}
	var out boardResult
	if res.NotModified {
		if err := s.Store.TouchBoard(ctx, b.ATS, b.Slug, seenAt); err != nil {
			return fail("store", err.Error())
		}
		res.ETag = b.ETag
	} else {
		// Safety valve: an empty listing for a board that used to have jobs is more likely an
		// upstream glitch than a mass closure, so nothing is closed in that case.
		complete := len(res.Jobs) > 0 || b.JobCount == 0
		if out.st, err = s.Store.ApplyBoard(ctx, b.ATS, b.Slug, res.Jobs, seenAt, complete); err != nil {
			return fail("store", err.Error())
		}
	}
	count := len(res.Jobs)
	if res.NotModified {
		count = b.JobCount
	}
	if err := s.Store.SetBoardResult(ctx, b.ATS, b.Slug, "ok", "", res.ETag, count); err != nil {
		s.Log.Error("record board result", "err", err)
	}
	s.learnName(ctx, p, b, res.BoardName)
	out.enriched = s.enrich(ctx, p, b)
	s.Log.Debug("board scanned", "ats", b.ATS, "board", b.Slug, "jobs", count, "added", out.st.Added, "closed", out.st.Closed,
		"not_modified", res.NotModified, "dur", time.Since(start).Round(time.Millisecond))
	return out
}

func (s *Service) learnName(ctx context.Context, p scrape.Provider, b store.Board, fromFeed string) {
	if b.DisplayName != "" {
		return
	}
	name := fromFeed
	if name == "" {
		if n, ok := p.(scrape.BoardNamer); ok {
			var err error
			if name, err = n.BoardName(ctx, s.Client, b.Slug); err != nil {
				s.Log.Debug("board name lookup failed", "board", b.Slug, "err", err)
				return
			}
		}
	}
	if err := s.Store.SetDisplayNameIfEmpty(ctx, b.ATS, b.Slug, name); err != nil {
		s.Log.Warn("set display name", "board", b.Slug, "err", err)
	}
}

func (s *Service) enrich(ctx context.Context, p scrape.Provider, b store.Board) int {
	df, ok := p.(scrape.DescriptionFetcher)
	if !ok || s.EnrichBudget <= 0 {
		return 0
	}
	missing, err := s.Store.MissingDescriptions(ctx, b.ATS, b.Slug, s.EnrichBudget)
	if err != nil {
		s.Log.Warn("list missing descriptions", "err", err)
		return 0
	}
	n := 0
	for ext, id := range missing {
		if ctx.Err() != nil {
			break
		}
		desc, err := df.FetchDescription(ctx, s.Client, b.Slug, ext)
		if err != nil || desc == "" {
			continue
		}
		if err := s.Store.SetDescription(ctx, id, desc); err != nil {
			s.Log.Warn("store description", "err", err)
			continue
		}
		n++
	}
	return n
}

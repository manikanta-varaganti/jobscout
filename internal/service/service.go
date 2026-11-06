// Package service is the application layer. Transports (HTTP, MCP, CLI) call it, never the scrapers directly.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/manikanta-varaganti/jobscout/internal/scrape"
	"github.com/manikanta-varaganti/jobscout/internal/store"
)

// Service wires the datastore to the scrapers and owns scan lifecycle.
type Service struct {
	Store        *store.Store
	Registry     scrape.Registry
	Client       *scrape.Client
	Workers      int
	EnrichBudget int // max per-posting description fetches per board per scan
	Log          *slog.Logger

	owner  string // identifies this process in cross-process leases
	base   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	progress ScanProgress
}

func New(st *store.Store, client *scrape.Client, workers int, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	if workers <= 0 {
		workers = 8
	}
	host, _ := os.Hostname()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	base, cancel := context.WithCancel(context.Background())
	return &Service{
		Store: st, Registry: scrape.Default(), Client: client, Workers: workers, EnrichBudget: 50, Log: log,
		owner: fmt.Sprintf("%s:%d:%s", host, os.Getpid(), hex.EncodeToString(b)),
		base:  base, cancel: cancel, progress: ScanProgress{State: "idle"},
	}
}

// Close cancels background work (a running scan) and waits for it to stop. Call it before
// closing the store.
func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

// SeedBoards inserts the embedded starter boards that are not yet known.
func (s *Service) SeedBoards(ctx context.Context) (added int, err error) {
	seeds, err := scrape.SeedBoards()
	if err != nil {
		return 0, err
	}
	for _, b := range seeds {
		if !scrape.ValidSlug(b.Slug) {
			s.Log.Warn("skipping invalid seed board", "ats", b.ATS, "slug", b.Slug)
			continue
		}
		ok, err := s.AddBoard(ctx, b.ATS, b.Slug)
		if err != nil {
			return added, err
		}
		if ok {
			added++
		}
	}
	return added, nil
}

// AddBoard validates and registers a board.
func (s *Service) AddBoard(ctx context.Context, ats, slug string) (bool, error) {
	if _, err := s.Registry.Get(ats); err != nil {
		return false, fmt.Errorf("%w: %v", store.ErrInvalid, err)
	}
	if !scrape.ValidSlug(slug) {
		return false, fmt.Errorf("%w: board slug %q", store.ErrInvalid, slug)
	}
	return s.Store.AddBoard(ctx, ats, slug)
}

// AddBoardURL detects the ATS and slug from a careers page or posting URL and registers the board.
func (s *Service) AddBoardURL(ctx context.Context, raw string) (ats, slug string, added bool, err error) {
	ats, slug, err = scrape.ParseBoardURL(raw)
	if err != nil {
		return "", "", false, fmt.Errorf("%w: %v", store.ErrInvalid, err)
	}
	added, err = s.AddBoard(ctx, ats, slug)
	return ats, slug, added, err
}

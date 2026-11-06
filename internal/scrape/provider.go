// Package scrape fetches job listings from public ATS JSON feeds (Ashby, Greenhouse, Lever).
package scrape

import (
	"context"
	"fmt"
	"sort"

	"github.com/manikanta-varaganti/jobscout/internal/domain"
)

// Result is one board's complete listing.
type Result struct {
	Jobs        []domain.Job
	ETag        string
	NotModified bool   // the feed is unchanged since prevETag; Jobs is empty
	BoardName   string // company name, when the feed includes it
}

// Provider fetches every open job of one board. Implementations must return the complete
// listing or an error, never a partial list, because the caller closes jobs missing from it.
type Provider interface {
	Name() string
	Fetch(ctx context.Context, c *Client, slug, prevETag string) (Result, error)
}

// BoardNamer is implemented by providers that can look up a board's company name separately.
type BoardNamer interface {
	BoardName(ctx context.Context, c *Client, slug string) (string, error)
}

// DescriptionFetcher is implemented by providers whose listing omits descriptions; the
// service fetches them per posting, for new jobs only, within a per-scan budget.
type DescriptionFetcher interface {
	FetchDescription(ctx context.Context, c *Client, slug, extID string) (string, error)
}

// Registry maps ATS name to provider.
type Registry map[string]Provider

// Default returns the built-in providers.
func Default() Registry {
	r := Registry{}
	for _, p := range []Provider{NewAshby(), NewGreenhouse(), NewLever(), NewRecruitee(), NewWorkable(), NewSmartRecruiters()} {
		r[p.Name()] = p
	}
	return r
}

func (r Registry) Names() []string {
	out := make([]string, 0, len(r))
	for k := range r {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (r Registry) Get(name string) (Provider, error) {
	p, ok := r[name]
	if !ok {
		return nil, fmt.Errorf("unsupported ATS %q (supported: %v)", name, r.Names())
	}
	return p, nil
}

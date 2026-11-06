package scrape

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/manikanta-varaganti/jobscout/internal/domain"
)

type Lever struct {
	BaseURL  string
	PageSize int
}

func NewLever() *Lever { return &Lever{BaseURL: "https://api.lever.co", PageSize: 100} }

func (*Lever) Name() string { return "lever" }

type leverJob struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	HostedURL  string `json:"hostedUrl"`
	CreatedAt  int64  `json:"createdAt"`
	Desc       string `json:"description"`
	DescPlain  string `json:"descriptionPlain"`
	Additional string `json:"additional"`
	AddPlain   string `json:"additionalPlain"`
	Workplace  string `json:"workplaceType"`
	Categories struct {
		Location   string `json:"location"`
		Commitment string `json:"commitment"`
		Department string `json:"department"`
		Team       string `json:"team"`
	} `json:"categories"`
	Lists []struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	} `json:"lists"`
}

// Fetch pages through the whole board. The first page tells us whether there are more; further
// pages are fetched concurrently in small waves (still bounded by the client's per-host limit).
// Lever has no conditional-request support, so prevETag is ignored.
func (l *Lever) Fetch(ctx context.Context, c *Client, slug, _ string) (Result, error) {
	size := l.PageSize
	if size <= 0 {
		size = 100
	}
	page := func(n int) ([]leverJob, error) {
		var batch []leverJob
		u := fmt.Sprintf("%s/v0/postings/%s?mode=json&skip=%d&limit=%d", l.BaseURL, url.PathEscape(slug), n*size, size)
		_, _, err := c.GetJSON(ctx, u, "", &batch)
		return batch, err
	}
	first, err := page(0)
	if err != nil {
		return Result{}, err
	}
	all := first
	const wave = 4
	for next, done := 1, len(first) < size; !done; next += wave {
		batches := make([][]leverJob, wave)
		errs := make([]error, wave)
		var wg sync.WaitGroup
		for i := 0; i < wave; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				batches[i], errs[i] = page(next + i)
			}(i)
		}
		wg.Wait()
		for i := 0; i < wave; i++ {
			if errs[i] != nil {
				return Result{}, errs[i]
			}
			all = append(all, batches[i]...)
			if len(batches[i]) < size {
				done = true
				break
			}
		}
	}
	out := Result{Jobs: make([]domain.Job, 0, len(all))}
	for _, j := range all {
		if j.ID == "" || j.HostedURL == "" || j.Text == "" {
			continue
		}
		parts := []string{HTMLToText(firstNonEmpty(j.Desc, j.DescPlain), false)}
		for _, li := range j.Lists {
			parts = append(parts, li.Text, HTMLToText(li.Content, false))
		}
		parts = append(parts, HTMLToText(firstNonEmpty(j.Additional, j.AddPlain), false))
		var pub string
		if j.CreatedAt > 0 {
			pub = time.UnixMilli(j.CreatedAt).UTC().Format(time.RFC3339)
		}
		out.Jobs = append(out.Jobs, domain.Job{
			ATS: "lever", Board: slug, ExtID: j.ID, Title: j.Text, Location: j.Categories.Location,
			Workplace: strings.ToLower(j.Workplace), EmploymentType: j.Categories.Commitment, Department: j.Categories.Department,
			Team: j.Categories.Team, URL: CanonicalURL(j.HostedURL), PublishedAt: pub,
			Description: strings.Join(strings.Fields(strings.Join(parts, " ")), " "),
		})
	}
	return out, nil
}

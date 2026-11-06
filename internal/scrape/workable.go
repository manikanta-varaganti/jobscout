package scrape

import (
	"context"
	"net/url"
	"strings"

	"github.com/manikanta-varaganti/jobscout/internal/domain"
)

// Workable reads the public widget API: https://apply.workable.com/api/v1/widget/accounts/{slug}?details=true
type Workable struct{ BaseURL string }

func NewWorkable() *Workable { return &Workable{BaseURL: "https://apply.workable.com"} }

func (*Workable) Name() string { return "workable" }

type workableJob struct {
	Title          string `json:"title"`
	Shortcode      string `json:"shortcode"`
	EmploymentType string `json:"employment_type"`
	Telecommuting  bool   `json:"telecommuting"`
	Department     string `json:"department"`
	URL            string `json:"url"`
	PublishedOn    string `json:"published_on"`
	CreatedAt      string `json:"created_at"`
	City           string `json:"city"`
	State          string `json:"state"`
	Country        string `json:"country"`
	Description    string `json:"description"`
}

func (w *Workable) Fetch(ctx context.Context, c *Client, slug, prevETag string) (Result, error) {
	var payload struct {
		Name string        `json:"name"`
		Jobs []workableJob `json:"jobs"`
	}
	etag, nm, err := c.GetJSON(ctx, w.BaseURL+"/api/v1/widget/accounts/"+url.PathEscape(slug)+"?details=true", prevETag, &payload)
	if err != nil {
		return Result{}, err
	}
	if nm {
		return Result{ETag: etag, NotModified: true}, nil
	}
	out := Result{ETag: etag, BoardName: strings.TrimSpace(payload.Name), Jobs: make([]domain.Job, 0, len(payload.Jobs))}
	for _, j := range payload.Jobs {
		if j.Shortcode == "" || j.Title == "" || j.URL == "" {
			continue
		}
		var loc []string
		for _, p := range []string{j.City, j.State, j.Country} {
			if p != "" {
				loc = append(loc, p)
			}
		}
		work := ""
		if j.Telecommuting {
			work = "remote"
		}
		out.Jobs = append(out.Jobs, domain.Job{
			ATS: "workable", Board: slug, ExtID: j.Shortcode, Title: j.Title, Location: strings.Join(loc, ", "), Workplace: work,
			EmploymentType: j.EmploymentType, Department: j.Department, URL: CanonicalURL(j.URL),
			PublishedAt: firstNonEmpty(j.PublishedOn, j.CreatedAt), Description: HTMLToText(j.Description, false),
		})
	}
	return out, nil
}

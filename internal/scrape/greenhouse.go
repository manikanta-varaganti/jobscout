package scrape

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/manikanta-varaganti/jobscout/internal/domain"
)

type Greenhouse struct{ BaseURL string }

func NewGreenhouse() *Greenhouse { return &Greenhouse{BaseURL: "https://boards-api.greenhouse.io"} }

func (*Greenhouse) Name() string { return "greenhouse" }

type ghJob struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	AbsoluteURL    string `json:"absolute_url"`
	UpdatedAt      string `json:"updated_at"`
	FirstPublished string `json:"first_published"`
	Location       struct {
		Name string `json:"name"`
	} `json:"location"`
	Departments []struct {
		Name string `json:"name"`
	} `json:"departments"`
	Content string `json:"content"`
}

func (g *Greenhouse) Fetch(ctx context.Context, c *Client, slug, prevETag string) (Result, error) {
	var payload struct {
		Jobs []ghJob `json:"jobs"`
	}
	etag, nm, err := c.GetJSON(ctx, g.BaseURL+"/v1/boards/"+url.PathEscape(slug)+"/jobs?content=true", prevETag, &payload)
	if err != nil {
		return Result{}, err
	}
	if nm {
		return Result{ETag: etag, NotModified: true}, nil
	}
	out := Result{ETag: etag, Jobs: make([]domain.Job, 0, len(payload.Jobs))}
	for _, j := range payload.Jobs {
		if j.ID == 0 || j.AbsoluteURL == "" || j.Title == "" {
			continue
		}
		depts := make([]string, 0, len(j.Departments))
		for _, d := range j.Departments {
			if d.Name != "" {
				depts = append(depts, d.Name)
			}
		}
		out.Jobs = append(out.Jobs, domain.Job{
			ATS: "greenhouse", Board: slug, ExtID: strconv.FormatInt(j.ID, 10), Title: j.Title, Location: j.Location.Name,
			Department: strings.Join(depts, ", "), URL: CanonicalURL(j.AbsoluteURL),
			PublishedAt: firstNonEmpty(j.FirstPublished, j.UpdatedAt), Description: HTMLToText(j.Content, true),
		})
	}
	return out, nil
}

// BoardName returns the company name Greenhouse shows for the board.
func (g *Greenhouse) BoardName(ctx context.Context, c *Client, slug string) (string, error) {
	var b struct {
		Name string `json:"name"`
	}
	_, _, err := c.GetJSON(ctx, g.BaseURL+"/v1/boards/"+url.PathEscape(slug), "", &b)
	return strings.TrimSpace(b.Name), err
}

package scrape

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/manikanta-varaganti/jobscout/internal/domain"
)

type Ashby struct{ BaseURL string }

func NewAshby() *Ashby { return &Ashby{BaseURL: "https://api.ashbyhq.com"} }

func (*Ashby) Name() string { return "ashby" }

type ashbyJob struct {
	ID               string          `json:"id"`
	Title            string          `json:"title"`
	Location         string          `json:"location"`
	EmploymentType   string          `json:"employmentType"`
	WorkplaceType    string          `json:"workplaceType"`
	IsRemote         bool            `json:"isRemote"`
	PublishedAt      string          `json:"publishedAt"`
	JobURL           string          `json:"jobUrl"`
	ApplyURL         string          `json:"applyUrl"`
	Department       string          `json:"department"`
	Team             string          `json:"team"`
	DescriptionHTML  string          `json:"descriptionHtml"`
	DescriptionPlain string          `json:"descriptionPlain"`
	Compensation     json.RawMessage `json:"compensation"`
}

func (a *Ashby) Fetch(ctx context.Context, c *Client, slug, prevETag string) (Result, error) {
	var payload struct {
		Jobs []ashbyJob `json:"jobs"`
	}
	etag, nm, err := c.GetJSON(ctx, a.BaseURL+"/posting-api/job-board/"+url.PathEscape(slug)+"?includeCompensation=true", prevETag, &payload)
	if err != nil {
		return Result{}, err
	}
	if nm {
		return Result{ETag: etag, NotModified: true}, nil
	}
	out := Result{ETag: etag, Jobs: make([]domain.Job, 0, len(payload.Jobs))}
	for _, j := range payload.Jobs {
		u := firstNonEmpty(j.JobURL, j.ApplyURL)
		if j.ID == "" || u == "" || j.Title == "" {
			continue
		}
		desc := j.DescriptionPlain
		if j.DescriptionHTML != "" {
			desc = HTMLToText(j.DescriptionHTML, false)
		}
		work := j.WorkplaceType
		if work == "" && j.IsRemote {
			work = "Remote"
		}
		out.Jobs = append(out.Jobs, domain.Job{
			ATS: "ashby", Board: slug, ExtID: j.ID, Title: j.Title, Location: j.Location, Workplace: strings.ToLower(work),
			EmploymentType: j.EmploymentType, Department: j.Department, Team: j.Team, Compensation: ashbyComp(j.Compensation),
			URL: CanonicalURL(u), PublishedAt: j.PublishedAt, Description: desc,
		})
	}
	return out, nil
}

func ashbyComp(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var c struct {
		Tier       string `json:"compensationTierSummary"`
		Scrapeable string `json:"scrapeableCompensationSalarySummary"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return ""
	}
	return firstNonEmpty(c.Tier, c.Scrapeable)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

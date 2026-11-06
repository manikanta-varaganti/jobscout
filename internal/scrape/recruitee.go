package scrape

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/manikanta-varaganti/jobscout/internal/domain"
)

// Recruitee reads https://{slug}.recruitee.com/api/offers/.
type Recruitee struct {
	BaseURL string // must contain %s for the slug
}

func NewRecruitee() *Recruitee { return &Recruitee{BaseURL: "https://%s.recruitee.com"} }

func (*Recruitee) Name() string { return "recruitee" }

var subdomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type recruiteeOffer struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	CareersURL     string `json:"careers_url"`
	Location       string `json:"location"`
	Remote         bool   `json:"remote"`
	Hybrid         bool   `json:"hybrid"`
	OnSite         bool   `json:"on_site"`
	EmploymentType string `json:"employment_type_code"`
	Department     string `json:"department"`
	PublishedAt    string `json:"published_at"`
	CreatedAt      string `json:"created_at"`
	Description    string `json:"description"`
	Requirements   string `json:"requirements"`
	Status         string `json:"status"`
}

func (r *Recruitee) Fetch(ctx context.Context, c *Client, slug, prevETag string) (Result, error) {
	if !subdomainRe.MatchString(slug) {
		return Result{}, &FetchError{Kind: KindParse, Msg: "recruitee slug must be a lowercase subdomain"}
	}
	var payload struct {
		Offers []recruiteeOffer `json:"offers"`
	}
	etag, nm, err := c.GetJSON(ctx, fmt.Sprintf(r.BaseURL, slug)+"/api/offers/", prevETag, &payload)
	if err != nil {
		return Result{}, err
	}
	if nm {
		return Result{ETag: etag, NotModified: true}, nil
	}
	out := Result{ETag: etag, Jobs: make([]domain.Job, 0, len(payload.Offers))}
	for _, o := range payload.Offers {
		if o.ID == 0 || o.Title == "" || o.CareersURL == "" || (o.Status != "" && o.Status != "published") {
			continue
		}
		work := ""
		switch {
		case o.Remote:
			work = "remote"
		case o.Hybrid:
			work = "hybrid"
		case o.OnSite:
			work = "on-site"
		}
		out.Jobs = append(out.Jobs, domain.Job{
			ATS: "recruitee", Board: slug, ExtID: strconv.FormatInt(o.ID, 10), Title: o.Title, Location: o.Location,
			Workplace: work, EmploymentType: strings.ReplaceAll(o.EmploymentType, "_", " "), Department: o.Department,
			URL: CanonicalURL(o.CareersURL), PublishedAt: firstNonEmpty(o.PublishedAt, o.CreatedAt),
			Description: strings.TrimSpace(HTMLToText(o.Description, false) + " " + HTMLToText(o.Requirements, false)),
		})
	}
	return out, nil
}

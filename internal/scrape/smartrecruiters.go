package scrape

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/manikanta-varaganti/jobscout/internal/domain"
)

// SmartRecruiters reads the public postings API. Listings carry no description; those are
// fetched per posting through FetchDescription.
type SmartRecruiters struct {
	BaseURL  string
	PageSize int
	MaxJobs  int // safety cap for very large employers
}

func NewSmartRecruiters() *SmartRecruiters {
	return &SmartRecruiters{BaseURL: "https://api.smartrecruiters.com", PageSize: 100, MaxJobs: 10000}
}

func (*SmartRecruiters) Name() string { return "smartrecruiters" }

type srLabel struct {
	Label string `json:"label"`
}

type srPosting struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Released string `json:"releasedDate"`
	Company  struct {
		Name string `json:"name"`
	} `json:"company"`
	Location struct {
		FullLocation string `json:"fullLocation"`
		City         string `json:"city"`
		Remote       bool   `json:"remote"`
		Hybrid       bool   `json:"hybrid"`
	} `json:"location"`
	Department       srLabel `json:"department"`
	TypeOfEmployment srLabel `json:"typeOfEmployment"`
}

type srPage struct {
	TotalFound int         `json:"totalFound"`
	Content    []srPosting `json:"content"`
}

func (s *SmartRecruiters) Fetch(ctx context.Context, c *Client, slug, _ string) (Result, error) {
	size := s.PageSize
	if size <= 0 {
		size = 100
	}
	get := func(off int) (srPage, error) {
		var p srPage
		_, _, err := c.GetJSON(ctx, fmt.Sprintf("%s/v1/companies/%s/postings?limit=%d&offset=%d", s.BaseURL, url.PathEscape(slug), size, off), "", &p)
		return p, err
	}
	first, err := get(0)
	if err != nil {
		return Result{}, err
	}
	total := first.TotalFound
	if s.MaxJobs > 0 && total > s.MaxJobs {
		return Result{}, &FetchError{Kind: KindParse, Msg: fmt.Sprintf("%d postings exceeds the %d cap; refusing a partial listing", total, s.MaxJobs)}
	}
	offsets := []int{}
	for off := size; off < total; off += size {
		offsets = append(offsets, off)
	}
	pages := make([]srPage, len(offsets))
	errs := make([]error, len(offsets))
	var wg sync.WaitGroup
	for i, off := range offsets {
		wg.Add(1)
		go func(i, off int) { // concurrency is bounded by the client's per-host semaphore
			defer wg.Done()
			pages[i], errs[i] = get(off)
		}(i, off)
	}
	wg.Wait()
	all := first.Content
	for i := range pages {
		if errs[i] != nil {
			return Result{}, errs[i]
		}
		all = append(all, pages[i].Content...)
	}
	out := Result{Jobs: make([]domain.Job, 0, len(all))}
	for _, p := range all {
		if p.ID == "" || p.Name == "" {
			continue
		}
		if out.BoardName == "" {
			out.BoardName = p.Company.Name
		}
		work := ""
		switch {
		case p.Location.Remote:
			work = "remote"
		case p.Location.Hybrid:
			work = "hybrid"
		}
		out.Jobs = append(out.Jobs, domain.Job{
			ATS: "smartrecruiters", Board: slug, ExtID: p.ID, Title: strings.TrimSpace(p.Name),
			Location:  strings.TrimSpace(strings.ReplaceAll(firstNonEmpty(p.Location.FullLocation, p.Location.City), ", ,", ",")),
			Workplace: work, EmploymentType: p.TypeOfEmployment.Label, Department: p.Department.Label,
			URL: "https://jobs.smartrecruiters.com/" + url.PathEscape(slug) + "/" + url.PathEscape(p.ID), PublishedAt: p.Released,
		})
	}
	return out, nil
}

// FetchDescription returns the plain-text job ad for one posting.
func (s *SmartRecruiters) FetchDescription(ctx context.Context, c *Client, slug, extID string) (string, error) {
	var d struct {
		JobAd struct {
			Sections map[string]struct {
				Title string `json:"title"`
				Text  string `json:"text"`
			} `json:"sections"`
		} `json:"jobAd"`
	}
	if _, _, err := c.GetJSON(ctx, fmt.Sprintf("%s/v1/companies/%s/postings/%s", s.BaseURL, url.PathEscape(slug), url.PathEscape(extID)), "", &d); err != nil {
		return "", err
	}
	var parts []string
	for _, k := range []string{"jobDescription", "qualifications", "additionalInformation", "companyDescription"} {
		if sec, ok := d.JobAd.Sections[k]; ok && sec.Text != "" {
			parts = append(parts, HTMLToText(sec.Text, false))
		}
	}
	return strings.Join(parts, " "), nil
}

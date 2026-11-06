package scrape

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseBoardURL recognises a careers page, posting or API URL and returns the ATS and board slug.
func ParseBoardURL(raw string) (ats, slug string, err error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", "", fmt.Errorf("not a URL: %q", raw)
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	parts := []string{}
	for _, p := range strings.Split(u.Path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	at := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	ok := func(a, s string) (string, string, error) {
		if !ValidSlug(s) {
			return "", "", fmt.Errorf("could not find a board name in %q", raw)
		}
		return a, s, nil
	}
	switch {
	case host == "jobs.ashbyhq.com":
		return ok("ashby", at(0))
	case host == "api.ashbyhq.com" && at(0) == "posting-api" && at(1) == "job-board":
		return ok("ashby", at(2))
	case host == "jobs.lever.co":
		return ok("lever", at(0))
	case host == "api.lever.co" && at(0) == "v0" && at(1) == "postings":
		return ok("lever", at(2))
	case host == "boards.greenhouse.io" || host == "job-boards.greenhouse.io":
		if at(0) == "embed" {
			return ok("greenhouse", u.Query().Get("for"))
		}
		return ok("greenhouse", at(0))
	case host == "boards-api.greenhouse.io" && at(0) == "v1" && at(1) == "boards":
		return ok("greenhouse", at(2))
	case strings.HasSuffix(host, ".recruitee.com"):
		return ok("recruitee", strings.TrimSuffix(host, ".recruitee.com"))
	case host == "apply.workable.com":
		if at(0) == "j" || at(0) == "api" {
			if at(0) == "api" && at(3) == "accounts" {
				return ok("workable", at(4))
			}
			return "", "", fmt.Errorf("a single Workable job link does not name the company; paste the careers page (apply.workable.com/<company>)")
		}
		return ok("workable", at(0))
	case host == "jobs.smartrecruiters.com" || host == "careers.smartrecruiters.com":
		return ok("smartrecruiters", at(0))
	case host == "api.smartrecruiters.com" && at(1) == "companies":
		return ok("smartrecruiters", at(2))
	}
	return "", "", fmt.Errorf("unrecognised job board URL %q (supported: Ashby, Greenhouse, Lever, Recruitee, Workable, SmartRecruiters)", raw)
}

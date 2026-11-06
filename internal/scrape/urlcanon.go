package scrape

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var (
	trackingKeys = map[string]bool{"gh_src": true, "source": true, "ref": true, "referrer": true, "lever-source": true, "lever-origin": true}
	multiSlash   = regexp.MustCompile(`/+`)
	applySuffix  = regexp.MustCompile(`/(apply|application)$`)
)

// CanonicalURL normalises a posting URL: lowercases the host, strips www., unifies Greenhouse hosts,
// drops fragments, tracking params and /apply suffixes. Job-identifying params such as gh_jid are kept.
func CanonicalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host == "boards.greenhouse.io" {
		host = "job-boards.greenhouse.io"
	}
	path := strings.TrimRight(multiSlash.ReplaceAllString(u.Path, "/"), "/")
	if strings.HasSuffix(host, "lever.co") || strings.HasSuffix(host, "ashbyhq.com") {
		path = applySuffix.ReplaceAllString(path, "")
	}
	if path == "" {
		path = "/"
	}
	q := u.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		lk := strings.ToLower(k)
		if trackingKeys[lk] || strings.HasPrefix(lk, "utm_") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	out := scheme + "://" + host + path
	if len(parts) > 0 {
		out += "?" + strings.Join(parts, "&")
	}
	return out
}

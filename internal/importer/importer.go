// Package importer loads the legacy Python scanner's ats-inventory-*.jsonl snapshots into the datastore.
package importer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/manikanta-varaganti/jobscout/internal/domain"
	"github.com/manikanta-varaganti/jobscout/internal/scrape"
	"github.com/manikanta-varaganti/jobscout/internal/store"
)

type record struct {
	ATS          string `json:"ats"`
	ExtID        any    `json:"ats_job_id"`
	Board        string `json:"company_board"`
	Title        string `json:"title"`
	Location     string `json:"location"`
	Workplace    string `json:"workplace_type"`
	Employment   string `json:"employment_type"`
	Department   string `json:"department"`
	Team         string `json:"team"`
	Description  string `json:"description"`
	URL          string `json:"posting_url"`
	PublishedAt  any    `json:"published_at"`
	FirstSeen    string `json:"first_seen"`
	ObservedAt   string `json:"observed_at"`
	Compensation any    `json:"compensation"`
}

type Summary struct {
	Files, Records, Skipped, Added int
}

func asString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprint(x)
	default:
		return fmt.Sprint(x)
	}
}

func publishedString(v any) string {
	if f, ok := v.(float64); ok { // legacy Lever rows stored epoch milliseconds
		if f > 1e11 {
			f /= 1000
		}
		return time.Unix(int64(f), 0).UTC().Format(time.RFC3339)
	}
	return asString(v)
}

func compString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		for _, k := range []string{"compensationTierSummary", "scrapeableCompensationSalarySummary"} {
			if s, ok := x[k].(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

type batch struct {
	ats, board string
	seenAt     string
	jobs       []domain.Job
}

type fileData struct {
	path    string
	seenAt  string
	batches map[string]*batch
}

func readFile(path string, sum *Summary) (*fileData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only: a close error cannot lose data
	fd := &fileData{path: path, batches: map[string]*batch{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			sum.Skipped++
			continue
		}
		id := asString(r.ExtID)
		if r.ATS == "" || r.Board == "" || id == "" || r.Title == "" || r.URL == "" {
			sum.Skipped++
			continue
		}
		seen := store.NormTime(r.ObservedAt)
		if seen == "" {
			seen = store.NormTime(r.FirstSeen)
		}
		if seen == "" {
			sum.Skipped++
			continue
		}
		ats := strings.ToLower(r.ATS)
		key := ats + "|" + strings.ToLower(r.Board)
		b := fd.batches[key]
		if b == nil {
			b = &batch{ats: ats, board: r.Board}
			fd.batches[key] = b
		}
		if seen > b.seenAt {
			b.seenAt = seen
		}
		if seen > fd.seenAt {
			fd.seenAt = seen
		}
		b.jobs = append(b.jobs, domain.Job{
			ATS: ats, Board: r.Board, ExtID: id, Title: r.Title, Location: r.Location, Workplace: strings.ToLower(r.Workplace),
			EmploymentType: r.Employment, Department: r.Department, Team: r.Team, Compensation: compString(r.Compensation),
			URL: r.URL, PublishedAt: publishedString(r.PublishedAt), Description: r.Description, FirstSeen: r.FirstSeen,
		})
		sum.Records++
	}
	return fd, sc.Err()
}

// peekSeen returns the observed_at of the first parsable record, used only to order files.
func peekSeen(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // read-only: a close error cannot lose data
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var r record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if t := store.NormTime(r.ObservedAt); t != "" {
			return t, nil
		}
	}
	return "", sc.Err()
}

// ImportJSONL imports snapshots in chronological order, one file in memory at a time. Only the newest
// snapshot is treated as a complete listing (so jobs that vanished before it are marked closed);
// older ones just add history.
func ImportJSONL(ctx context.Context, st *store.Store, paths []string, logf func(string, ...any)) (Summary, error) {
	var sum Summary
	type pf struct{ path, seen string }
	var files []pf
	for _, p := range paths {
		seen, err := peekSeen(p)
		if err != nil {
			return sum, fmt.Errorf("%s: %w", p, err)
		}
		files = append(files, pf{p, seen})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].seen < files[j].seen })
	for i, f := range files {
		fd, err := readFile(f.path, &sum)
		if err != nil {
			return sum, fmt.Errorf("%s: %w", f.path, err)
		}
		sum.Files++
		last := i == len(files)-1
		for _, b := range fd.batches {
			// Legacy data contains a few malformed slugs; keep their jobs but do not scan them as boards.
			if scrape.ValidSlug(b.board) {
				if _, err := st.AddBoard(ctx, b.ats, b.board); err != nil {
					return sum, err
				}
			}
			as, err := st.ApplyBoard(ctx, b.ats, b.board, b.jobs, b.seenAt, last)
			if err != nil {
				return sum, fmt.Errorf("%s %s/%s: %w", f.path, b.ats, b.board, err)
			}
			sum.Added += as.Added
		}
		if logf != nil {
			logf("imported %s (%d boards)", f.path, len(fd.batches))
		}
	}
	return sum, nil
}

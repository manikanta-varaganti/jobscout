package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/manikanta-varaganti/jobscout/internal/query"
)

var (
	// ErrBadQuery means the search expression could not be evaluated.
	ErrBadQuery = errors.New("invalid search query")
	// ErrBadCursor means the pagination cursor is malformed.
	ErrBadCursor = errors.New("invalid cursor")
)

// SearchParams filters, sorts and pages a search. The JSON form is what saved searches store.
type SearchParams struct {
	Q              string      `json:"q,omitempty"`
	Scope          query.Scope `json:"scope,omitempty"`
	ATS            string      `json:"ats,omitempty"`
	Workplace      string      `json:"workplace,omitempty"`
	Company        string      `json:"company,omitempty"`
	Status         string      `json:"status,omitempty"`
	Location       string      `json:"location,omitempty"`
	PostedDays     int         `json:"posted_days,omitempty"`
	TargetOnly     bool        `json:"target_only,omitempty"`
	NewToday       bool        `json:"new_today,omitempty"`
	IncludeClosed  bool        `json:"include_closed,omitempty"`
	FirstSeenAfter string      `json:"first_seen_after,omitempty"` // RFC3339; "new since"
	Sort           string      `json:"sort,omitempty"`             // published (default) | first_seen | company | title
	Group          bool        `json:"group,omitempty"`            // one row per company + title, with all locations

	Limit  int    `json:"-"`
	Cursor string `json:"-"`
	All    bool   `json:"-"` // no page limit (exports)
}

// JobRow is a job as returned to clients.
type JobRow struct {
	ID             int64    `json:"id"`
	ATS            string   `json:"ats"`
	Company        string   `json:"company"`      // board slug
	CompanyName    string   `json:"company_name"` // display name
	ExtID          string   `json:"ext_id"`
	Title          string   `json:"title"`
	Location       string   `json:"location"`
	Workplace      string   `json:"workplace"`
	EmploymentType string   `json:"employment_type,omitempty"`
	Department     string   `json:"department,omitempty"`
	Team           string   `json:"team,omitempty"`
	Compensation   string   `json:"compensation,omitempty"`
	URL            string   `json:"url"`
	PublishedAt    string   `json:"published_at,omitempty"`
	FirstSeen      string   `json:"first_seen"`
	LastSeen       string   `json:"last_seen"`
	ClosedAt       string   `json:"closed_at,omitempty"`
	IsTarget       bool     `json:"is_target"`
	Status         string   `json:"status,omitempty"`
	Postings       int      `json:"postings,omitempty"`  // grouped results: postings in the group
	Locations      []string `json:"locations,omitempty"` // grouped results: distinct locations
	Tags           []string `json:"tags,omitempty"`
	Description    string   `json:"description,omitempty"`
	Notes          []Note   `json:"notes,omitempty"`
}

type Note struct {
	TS    string `json:"ts"`
	Actor string `json:"actor"`
	Note  string `json:"note"`
}

type Facet struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	Count int    `json:"count"`
}

type SearchResult struct {
	Total        int      `json:"total"`  // matching postings
	Groups       int      `json:"groups"` // rows available when grouped (= total otherwise)
	Companies    int      `json:"companies"`
	Posted7Days  int      `json:"posted_7d"`
	NewToday     int      `json:"new_today"`
	Jobs         []JobRow `json:"jobs"`
	NextCursor   string   `json:"next_cursor,omitempty"`
	TopCompanies []Facet  `json:"top_companies"`
	PerDay       []Facet  `json:"per_day"` // last 30 days by published date (UTC), oldest first
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// StartOfLocalDay is midnight today in the machine's time zone, as a store timestamp.
func StartOfLocalDay() string {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, n.Location()).UTC().Format(timeFmt)
}

func daysAgo(d int) string { return time.Now().UTC().AddDate(0, 0, -d).Format(timeFmt) }

func buildWhere(p SearchParams) (string, []any) {
	var conds []string
	var args []any
	add := func(c string, a ...any) { conds = append(conds, c); args = append(args, a...) }
	if !p.IncludeClosed {
		add("j.closed_at IS NULL")
	}
	if p.TargetOnly {
		add("j.is_target = 1")
	}
	if p.ATS != "" {
		add("j.ats = ? COLLATE NOCASE", p.ATS)
	}
	if p.Workplace != "" {
		add("j.workplace = ? COLLATE NOCASE", p.Workplace)
	}
	if p.Company != "" {
		add("j.board = ?", p.Company)
	}
	if p.Status != "" {
		add("j.status = ?", p.Status)
	}
	if p.Location != "" {
		add(`j.location LIKE ? ESCAPE '\' COLLATE NOCASE`, "%"+escapeLike(p.Location)+"%")
	}
	if p.PostedDays > 0 {
		add("j.published_at >= ?", daysAgo(p.PostedDays))
	}
	if p.NewToday {
		add("j.first_seen >= ?", StartOfLocalDay())
	}
	if t := NormTime(p.FirstSeenAfter); t != "" {
		add("j.first_seen > ?", t)
	}
	scope := p.Scope
	if scope == "" {
		scope = query.ScopeTitle
	}
	if qsql, qargs := query.Compile(p.Q, scope); qsql != "" {
		add(qsql, qargs...)
	}
	if len(conds) == 0 {
		return "1", nil
	}
	return strings.Join(conds, " AND "), args
}

type light struct {
	id                        int64
	ats, board, pub, first, t string
	key                       string // group key
}

func encodeCursor(off int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("o" + strconv.Itoa(off)))
}

func decodeCursor(c string) (int, error) {
	if c == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || len(b) < 2 || b[0] != 'o' {
		return 0, ErrBadCursor
	}
	n, err := strconv.Atoi(string(b[1:]))
	if err != nil || n < 0 {
		return 0, ErrBadCursor
	}
	return n, nil
}

// Search evaluates the filter once, then computes counts, facets, grouping and the page in memory.
func (s *Store) Search(ctx context.Context, p SearchParams) (SearchResult, error) {
	res := SearchResult{Jobs: []JobRow{}, TopCompanies: []Facet{}, PerDay: []Facet{}}
	offset, err := decodeCursor(p.Cursor)
	if err != nil {
		return res, err
	}
	if p.Limit <= 0 || p.Limit > 500 {
		p.Limit = 50
	}
	where, args := buildWhere(p)
	// where is assembled from fixed fragments and "?" placeholders only (see buildWhere and query.Compile).
	rows, err := s.r.QueryContext(ctx, `SELECT j.id, j.ats, j.board, COALESCE(j.published_at,''), j.first_seen, j.title FROM jobs j WHERE `+where, args...) //nolint:gosec // G202
	if err != nil {
		return res, classify(err)
	}
	var all []light
	for rows.Next() {
		var l light
		if err := rows.Scan(&l.id, &l.ats, &l.board, &l.pub, &l.first, &l.t); err != nil {
			rows.Close()
			return res, err
		}
		all = append(all, l)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return res, classify(err)
	}
	rows.Close()

	// counts and facets
	week, today, month := daysAgo(7), StartOfLocalDay(), daysAgo(30)
	companies := map[string]int{}
	perDay := map[string]int{}
	for _, l := range all {
		companies[l.ats+"|"+l.board]++
		if l.pub >= week {
			res.Posted7Days++
		}
		if l.first >= today {
			res.NewToday++
		}
		if l.pub >= month {
			perDay[l.pub[:10]]++
		}
	}
	res.Total, res.Companies = len(all), len(companies)
	for i := 29; i >= 0; i-- {
		d := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		res.PerDay = append(res.PerDay, Facet{Name: d, Count: perDay[d]})
	}
	for k, n := range companies {
		res.TopCompanies = append(res.TopCompanies, Facet{Name: k[strings.IndexByte(k, '|')+1:], Label: k, Count: n})
	}
	sort.Slice(res.TopCompanies, func(i, j int) bool {
		a, b := res.TopCompanies[i], res.TopCompanies[j]
		return a.Count > b.Count || (a.Count == b.Count && a.Label < b.Label)
	})
	if len(res.TopCompanies) > 10 {
		res.TopCompanies = res.TopCompanies[:10]
	}
	if err := s.labelCompanies(ctx, res.TopCompanies); err != nil {
		return res, err
	}

	// sort
	less := func(a, b light) bool {
		switch p.Sort {
		case "first_seen":
			if a.first != b.first {
				return a.first > b.first
			}
		case "company":
			if x, y := strings.ToLower(a.board), strings.ToLower(b.board); x != y {
				return x < y
			}
			if x, y := strings.ToLower(a.t), strings.ToLower(b.t); x != y {
				return x < y
			}
		case "title":
			if x, y := strings.ToLower(a.t), strings.ToLower(b.t); x != y {
				return x < y
			}
		default:
			if a.pub != b.pub {
				return a.pub > b.pub // empty dates sort last
			}
		}
		return a.id > b.id
	}
	sort.Slice(all, func(i, j int) bool { return less(all[i], all[j]) })

	// group
	members := map[string][]int64{}
	list := all
	if p.Group {
		list = list[:0:0]
		for _, l := range all {
			l.key = l.ats + "|" + strings.ToLower(l.board) + "|" + strings.ToLower(strings.Join(strings.Fields(l.t), " "))
			if _, seen := members[l.key]; !seen {
				list = append(list, l)
			}
			members[l.key] = append(members[l.key], l.id)
		}
	}
	res.Groups = len(list)

	// page
	if offset > len(list) {
		offset = len(list)
	}
	end := len(list)
	if !p.All && offset+p.Limit < end {
		end = offset + p.Limit
		res.NextCursor = encodeCursor(end)
	}
	page := list[offset:end]
	ids := make([]int64, len(page))
	for i, l := range page {
		ids[i] = l.id
	}
	byID, err := s.jobsByID(ctx, ids)
	if err != nil {
		return res, err
	}
	for _, l := range page {
		j, ok := byID[l.id]
		if !ok {
			continue
		}
		if p.Group {
			m := members[l.key]
			j.Postings = len(m)
			if len(m) > 1 {
				if j.Locations, err = s.locations(ctx, m); err != nil {
					return res, err
				}
			}
		}
		res.Jobs = append(res.Jobs, j)
	}
	return res, nil
}

func classify(err error) error {
	m := err.Error()
	if strings.Contains(m, "fts5") || strings.Contains(m, "MATCH") || strings.Contains(m, "unterminated") || strings.Contains(m, "syntax error") {
		return fmt.Errorf("%w: %v", ErrBadQuery, err)
	}
	return err
}

func idsJSON(ids []int64) string {
	b, _ := json.Marshal(ids)
	return string(b)
}

const jobCols = `j.id, j.ats, j.board, COALESCE(b.display_name,''), j.ext_id, j.title, j.location, j.workplace, j.employment_type,
	j.department, j.team, j.compensation, j.url, COALESCE(j.published_at,''), j.first_seen, j.last_seen, COALESCE(j.closed_at,''), j.is_target, j.status`

const jobFrom = ` FROM jobs j LEFT JOIN boards b ON b.ats=j.ats AND b.slug=j.board `

func scanJob(r interface{ Scan(...any) error }) (JobRow, error) {
	var j JobRow
	err := r.Scan(&j.ID, &j.ATS, &j.Company, &j.CompanyName, &j.ExtID, &j.Title, &j.Location, &j.Workplace, &j.EmploymentType,
		&j.Department, &j.Team, &j.Compensation, &j.URL, &j.PublishedAt, &j.FirstSeen, &j.LastSeen, &j.ClosedAt, &j.IsTarget, &j.Status)
	if j.CompanyName == "" {
		j.CompanyName = Humanize(j.Company)
	}
	return j, err
}

func (s *Store) jobsByID(ctx context.Context, ids []int64) (map[int64]JobRow, error) {
	out := make(map[int64]JobRow, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.r.QueryContext(ctx, `SELECT `+jobCols+jobFrom+`WHERE j.id IN (SELECT value FROM json_each(?))`, idsJSON(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out[j.ID] = j
	}
	return out, rows.Err()
}

func (s *Store) locations(ctx context.Context, ids []int64) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT DISTINCT location FROM jobs WHERE id IN (SELECT value FROM json_each(?)) AND location<>'' ORDER BY location`, idsJSON(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// labelCompanies fills Facet.Label (input "ats|slug") with the display name.
func (s *Store) labelCompanies(ctx context.Context, fs []Facet) error {
	for i := range fs {
		ats, slug, _ := strings.Cut(fs[i].Label, "|")
		var name string
		err := s.r.QueryRowContext(ctx, `SELECT display_name FROM boards WHERE ats=? AND slug=?`, ats, slug).Scan(&name)
		if err != nil && !errors.Is(notFound(err), ErrNotFound) {
			return err
		}
		if name == "" {
			name = Humanize(slug)
		}
		fs[i].Label = name
	}
	return nil
}

func (s *Store) facet(ctx context.Context, q string, args ...any) ([]Facet, error) {
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Facet{}
	for rows.Next() {
		var f Facet
		if err := rows.Scan(&f.Name, &f.Count); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Options returns distinct values for filter dropdowns.
func (s *Store) Options(ctx context.Context) (ats, workplace []Facet, err error) {
	if ats, err = s.facet(ctx, `SELECT ats, COUNT(*) FROM jobs WHERE closed_at IS NULL GROUP BY ats ORDER BY 2 DESC`); err != nil {
		return nil, nil, err
	}
	workplace, err = s.facet(ctx, `SELECT workplace, COUNT(*) FROM jobs WHERE closed_at IS NULL AND workplace<>'' GROUP BY workplace ORDER BY 2 DESC`)
	return ats, workplace, err
}

// GetJob returns one job with description, tags and notes.
func (s *Store) GetJob(ctx context.Context, id int64) (JobRow, error) {
	j, err := scanJob(s.r.QueryRowContext(ctx, `SELECT `+jobCols+jobFrom+`WHERE j.id=?`, id))
	if err != nil {
		return j, notFound(err)
	}
	var plain string
	var z []byte
	if err := s.r.QueryRowContext(ctx, `SELECT description, description_z FROM job_text WHERE job_id=?`, id).Scan(&plain, &z); err != nil && !errors.Is(notFound(err), ErrNotFound) {
		return j, err
	}
	if j.Description = plain; plain == "" {
		if j.Description, err = decompress(z); err != nil {
			return j, err
		}
	}
	rows, err := s.r.QueryContext(ctx, `SELECT tag FROM job_tags WHERE job_id=? ORDER BY tag`, id)
	if err != nil {
		return j, err
	}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return j, err
		}
		j.Tags = append(j.Tags, t)
	}
	rows.Close()
	rows, err = s.r.QueryContext(ctx, `SELECT ts, actor, note FROM job_notes WHERE job_id=? ORDER BY id`, id)
	if err != nil {
		return j, err
	}
	defer rows.Close()
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.TS, &n.Actor, &n.Note); err != nil {
			return j, err
		}
		j.Notes = append(j.Notes, n)
	}
	return j, rows.Err()
}

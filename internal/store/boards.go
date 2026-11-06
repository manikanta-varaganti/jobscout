package store

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"unicode"
)

// DisableAfterNotFound is how many consecutive 404s retire a board from scanning.
const DisableAfterNotFound = 5

// Board is a job board on one ATS.
type Board struct {
	ATS                 string `json:"ats"`
	Slug                string `json:"slug"`
	DisplayName         string `json:"display_name"`
	LastStatus          string `json:"last_status"`
	LastError           string `json:"last_error,omitempty"`
	LastScannedAt       string `json:"last_scanned_at,omitempty"`
	JobCount            int    `json:"job_count"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	Disabled            bool   `json:"disabled"`
	ETag                string `json:"-"`
}

// Name is the display name, or a readable form of the slug.
func (b Board) Name() string {
	if b.DisplayName != "" {
		return b.DisplayName
	}
	return Humanize(b.Slug)
}

var camelRe = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// Humanize turns a slug like "ScribdInc" or "flow-engineering" into "Scribd Inc" / "Flow Engineering".
func Humanize(slug string) string {
	s := camelRe.ReplaceAllString(slug, "$1 $2")
	s = strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(s)
	words := strings.Fields(s)
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

// AddBoard registers a board if it is not already known. It reports whether it was new.
func (s *Store) AddBoard(ctx context.Context, ats, slug string) (bool, error) {
	res, err := s.w.ExecContext(ctx, `INSERT OR IGNORE INTO boards (ats, slug, added_at) VALUES (?,?,?)`, ats, slug, Now())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ListBoards returns boards; disabled ones only when includeDisabled is set.
func (s *Store) ListBoards(ctx context.Context, includeDisabled bool) ([]Board, error) {
	q := `SELECT ats, slug, display_name, COALESCE(last_status,''), COALESCE(last_error,''), COALESCE(etag,''),
		COALESCE(last_scanned_at,''), job_count, consecutive_failures, disabled FROM boards`
	if !includeDisabled {
		q += ` WHERE disabled = 0`
	}
	rows, err := s.r.QueryContext(ctx, q+` ORDER BY ats, slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Board{}
	for rows.Next() {
		var b Board
		if err := rows.Scan(&b.ATS, &b.Slug, &b.DisplayName, &b.LastStatus, &b.LastError, &b.ETag, &b.LastScannedAt,
			&b.JobCount, &b.ConsecutiveFailures, &b.Disabled); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SetBoardResult records a scan outcome. Consecutive 404s eventually disable the board.
func (s *Store) SetBoardResult(ctx context.Context, ats, slug, status, errMsg, etag string, count int) error {
	_, err := s.w.ExecContext(ctx, `UPDATE boards SET last_scanned_at=?, last_status=?, last_error=?,
		etag = CASE WHEN ?<>'' THEN ? ELSE etag END,
		job_count = CASE WHEN ?='ok' THEN ? ELSE job_count END,
		consecutive_failures = CASE WHEN ?='ok' THEN 0 ELSE consecutive_failures + 1 END,
		disabled = CASE WHEN ?='not_found' AND consecutive_failures + 1 >= ? THEN 1 ELSE disabled END
		WHERE ats=? AND slug=?`,
		Now(), status, errMsg, etag, etag, status, count, status, status, DisableAfterNotFound, ats, slug)
	return err
}

// SetBoardEnabled enables or disables scanning of a board.
func (s *Store) SetBoardEnabled(ctx context.Context, ats, slug string, enabled bool) error {
	d := 1
	if enabled {
		d = 0
	}
	res, err := s.w.ExecContext(ctx, `UPDATE boards SET disabled=?, consecutive_failures=CASE WHEN ?=0 THEN 0 ELSE consecutive_failures END
		WHERE ats=? AND slug=?`, d, d, ats, slug)
	return affected(res, err)
}

// SetDisplayName sets the company name shown for a board and re-indexes its jobs so the
// name is searchable. An empty name reverts to the humanized slug.
func (s *Store) SetDisplayName(ctx context.Context, ats, slug, name string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE boards SET display_name=? WHERE ats=? AND slug=?`, strings.TrimSpace(name), ats, slug)
		if err := affected(res, err); err != nil {
			return err
		}
		company, err := companyTerms(tx, ats, slug)
		if err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT j.id, j.title, j.location, j.department, j.team, t.description, t.description_z
			FROM jobs j JOIN job_text t ON t.job_id=j.id WHERE j.ats=? AND j.board=?`, ats, slug)
		if err != nil {
			return err
		}
		type item struct {
			id int64
			j  Job
		}
		var items []item
		for rows.Next() {
			var it item
			var z []byte
			if err := rows.Scan(&it.id, &it.j.Title, &it.j.Location, &it.j.Department, &it.j.Team, &it.j.Description, &z); err != nil {
				rows.Close()
				return err
			}
			if it.j.Description == "" {
				if it.j.Description, err = decompress(z); err != nil {
					rows.Close()
					return err
				}
			}
			items = append(items, it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, it := range items {
			if err := ftsReplace(tx, it.id, it.j, company); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetDisplayNameIfEmpty records a name learned from a feed without overriding a user's choice.
func (s *Store) SetDisplayNameIfEmpty(ctx context.Context, ats, slug, name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	var cur string
	if err := s.r.QueryRowContext(ctx, `SELECT display_name FROM boards WHERE ats=? AND slug=?`, ats, slug).Scan(&cur); err != nil {
		return notFound(err)
	}
	if cur != "" {
		return nil
	}
	return s.SetDisplayName(ctx, ats, slug, name)
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

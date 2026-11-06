package store

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"strings"
	"time"

	"github.com/manikanta-varaganti/jobscout/internal/domain"
	"github.com/manikanta-varaganti/jobscout/internal/titlefilter"
)

// Job is the shared domain type.
type Job = domain.Job

// ApplyStats summarises one board's upsert.
type ApplyStats struct{ Seen, Added, Updated, Reopened, Closed int }

// NormTime converts common timestamp forms to UTC RFC3339 ("" when unparseable).
func NormTime(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z0700", "2006-01-02T15:04:05Z0700",
		"2006-01-02 15:04:05 MST", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(timeFmt)
		}
	}
	return ""
}

func contentHash(j Job) string {
	h := sha256.New()
	for _, f := range []string{j.Title, j.Location, j.Workplace, j.EmploymentType, j.Department, j.Team, j.Compensation, j.URL, j.Description} {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// compress / decompress descriptions (zlib roughly quarters the size of job text).
func compress(s string) []byte {
	if s == "" {
		return nil
	}
	var b bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&b, zlib.BestSpeed)
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()
	return b.Bytes()
}

func decompress(z []byte) (string, error) {
	if len(z) == 0 {
		return "", nil
	}
	zr, err := zlib.NewReader(bytes.NewReader(z))
	if err != nil {
		return "", err
	}
	defer zr.Close()
	b, err := io.ReadAll(zr)
	return string(b), err
}

func addEvent(tx *sql.Tx, ts, kind string, jobID int64, actor, data string) error {
	_, err := tx.Exec(`INSERT INTO events (ts, kind, job_id, actor, data) VALUES (?,?,?,?,?)`, ts, kind, jobID, actor, data)
	return err
}

func ftsPut(tx *sql.Tx, id int64, j Job, company string) error {
	_, err := tx.Exec(`INSERT INTO jobs_fts (rowid, title, company, location, department, description) VALUES (?,?,?,?,?,?)`,
		id, j.Title, company, j.Location, j.Department+" "+j.Team, j.Description)
	return err
}

func ftsReplace(tx *sql.Tx, id int64, j Job, company string) error {
	if _, err := tx.Exec(`DELETE FROM jobs_fts WHERE rowid=?`, id); err != nil {
		return err
	}
	return ftsPut(tx, id, j, company)
}

// companyTerms is what the FTS company column holds: the slug plus the display name.
func companyTerms(tx *sql.Tx, ats, board string) (string, error) {
	var name string
	err := tx.QueryRow(`SELECT display_name FROM boards WHERE ats=? AND slug=?`, ats, board).Scan(&name)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	return strings.TrimSpace(board + " " + name + " " + Humanize(board)), nil
}

type existing struct {
	id     int64
	hash   string
	closed bool
}

// ApplyBoard upserts every job of one board in a single transaction. When complete is true
// (the board's full listing was fetched), open jobs absent from the listing are marked closed.
func (s *Store) ApplyBoard(ctx context.Context, ats, board string, jobs []Job, seenAt string, complete bool) (ApplyStats, error) {
	var st ApplyStats
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		const actor = "scanner"
		company, err := companyTerms(tx, ats, board)
		if err != nil {
			return err
		}
		// One query for the board's current state instead of one per job.
		known := map[string]existing{}
		rows, err := tx.Query(`SELECT ext_id, id, content_hash, closed_at IS NOT NULL FROM jobs WHERE ats=? AND board=?`, ats, board)
		if err != nil {
			return err
		}
		for rows.Next() {
			var ext string
			var e existing
			if err := rows.Scan(&ext, &e.id, &e.hash, &e.closed); err != nil {
				rows.Close()
				return err
			}
			known[ext] = e
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		present := make(map[string]struct{}, len(jobs))
		for _, j := range jobs {
			if _, dup := present[j.ExtID]; dup {
				continue // a feed listing the same id twice
			}
			present[j.ExtID] = struct{}{}
			j.ATS, j.Board = ats, board
			st.Seen++
			hash := contentHash(j)
			pub := NormTime(j.PublishedAt)
			target := 0
			if titlefilter.Matches(j.Title, j.Description) {
				target = 1
			}
			e, ok := known[j.ExtID]
			switch {
			case !ok:
				first := seenAt
				if f := NormTime(j.FirstSeen); f != "" {
					first = f
				}
				res, err := tx.Exec(`INSERT INTO jobs (ats, board, ext_id, title, location, workplace, employment_type, department, team, compensation, url,
					published_at, first_seen, last_seen, content_hash, is_target) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
					ats, board, j.ExtID, j.Title, j.Location, j.Workplace, j.EmploymentType, j.Department, j.Team, j.Compensation, j.URL,
					nullStr(pub), first, seenAt, hash, target)
				if err != nil {
					return err
				}
				id, err := res.LastInsertId()
				if err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT INTO job_text (job_id, description, description_z) VALUES (?,'',?)`, id, compress(j.Description)); err != nil {
					return err
				}
				if err := ftsPut(tx, id, j, company); err != nil {
					return err
				}
				if err := addEvent(tx, seenAt, "added", id, actor, ""); err != nil {
					return err
				}
				st.Added++
			case e.hash != hash:
				if _, err := tx.Exec(`UPDATE jobs SET title=?, location=?, workplace=?, employment_type=?, department=?, team=?, compensation=?, url=?,
					published_at=COALESCE(?, published_at), last_seen=?, closed_at=NULL, content_hash=?, is_target=? WHERE id=?`,
					j.Title, j.Location, j.Workplace, j.EmploymentType, j.Department, j.Team, j.Compensation, j.URL,
					nullStr(pub), seenAt, hash, target, e.id); err != nil {
					return err
				}
				if _, err := tx.Exec(`UPDATE job_text SET description='', description_z=? WHERE job_id=?`, compress(j.Description), e.id); err != nil {
					return err
				}
				if err := ftsReplace(tx, e.id, j, company); err != nil {
					return err
				}
				kind := "updated"
				if e.closed {
					kind = "reopened"
					st.Reopened++
				} else {
					st.Updated++
				}
				if err := addEvent(tx, seenAt, kind, e.id, actor, ""); err != nil {
					return err
				}
			default:
				if _, err := tx.Exec(`UPDATE jobs SET last_seen=?, closed_at=NULL WHERE id=?`, seenAt, e.id); err != nil {
					return err
				}
				if e.closed {
					st.Reopened++
					if err := addEvent(tx, seenAt, "reopened", e.id, actor, ""); err != nil {
						return err
					}
				}
			}
		}
		if complete {
			for ext, e := range known {
				if _, ok := present[ext]; ok || e.closed {
					continue
				}
				if _, err := tx.Exec(`UPDATE jobs SET closed_at=? WHERE id=?`, seenAt, e.id); err != nil {
					return err
				}
				if err := addEvent(tx, seenAt, "closed", e.id, actor, ""); err != nil {
					return err
				}
				st.Closed++
			}
		}
		return nil
	})
	return st, err
}

// TouchBoard marks every open job of a board as seen (used when the feed is unchanged: HTTP 304).
func (s *Store) TouchBoard(ctx context.Context, ats, board, seenAt string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE jobs SET last_seen=? WHERE ats=? AND board=? AND closed_at IS NULL`, seenAt, ats, board)
	return err
}

// MissingDescriptions returns open jobs of a board that have no description yet (ext_id -> id),
// newest first, for providers that need a per-posting request to get one.
func (s *Store) MissingDescriptions(ctx context.Context, ats, board string, limit int) (map[string]int64, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT j.ext_id, j.id FROM jobs j JOIN job_text t ON t.job_id=j.id
		WHERE j.ats=? AND j.board=? AND j.closed_at IS NULL AND t.description='' AND t.description_z IS NULL
		ORDER BY j.first_seen DESC LIMIT ?`, ats, board, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var ext string
		var id int64
		if err := rows.Scan(&ext, &id); err != nil {
			return nil, err
		}
		out[ext] = id
	}
	return out, rows.Err()
}

// SetDescription stores a description fetched after the listing and re-indexes the job.
func (s *Store) SetDescription(ctx context.Context, id int64, desc string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var j Job
		if err := tx.QueryRow(`SELECT ats, board, title, location, department, team FROM jobs WHERE id=?`, id).
			Scan(&j.ATS, &j.Board, &j.Title, &j.Location, &j.Department, &j.Team); err != nil {
			return notFound(err)
		}
		j.Description = desc
		if _, err := tx.Exec(`UPDATE job_text SET description='', description_z=? WHERE job_id=?`, compress(desc), id); err != nil {
			return err
		}
		target := 0
		if titlefilter.Matches(j.Title, desc) {
			target = 1
		}
		if _, err := tx.Exec(`UPDATE jobs SET is_target=? WHERE id=?`, target, id); err != nil {
			return err
		}
		company, err := companyTerms(tx, j.ATS, j.Board)
		if err != nil {
			return err
		}
		return ftsReplace(tx, id, j, company)
	})
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func notFound(err error) error {
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	return err
}

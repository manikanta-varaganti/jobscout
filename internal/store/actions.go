package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ErrInvalid wraps input validation failures.
var ErrInvalid = errors.New("invalid input")

// Write describes who is changing data, and an optional idempotency key so a retried
// request is applied at most once.
type Write struct {
	Actor      string
	RequestKey string
}

func (s *Store) writeJob(ctx context.Context, id int64, kind string, w Write, data string, fn func(tx *sql.Tx) error) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if w.RequestKey != "" {
			var one int
			err := tx.QueryRow(`SELECT 1 FROM events WHERE request_key=?`, w.RequestKey).Scan(&one)
			if err == nil {
				return nil // already applied
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		var one int
		if err := tx.QueryRow(`SELECT 1 FROM jobs WHERE id=?`, id).Scan(&one); err != nil {
			return notFound(err)
		}
		if err := fn(tx); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO events (ts, kind, job_id, actor, data, request_key) VALUES (?,?,?,?,?,?)`,
			Now(), kind, id, w.Actor, data, nullStr(w.RequestKey))
		return err
	})
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
}

// SetStatus sets the user-workflow status ("" clears it).
func (s *Store) SetStatus(ctx context.Context, id int64, status string, w Write) error {
	status = strings.TrimSpace(status)
	if utf8.RuneCountInString(status) > 40 {
		return invalid("status must be at most 40 characters")
	}
	return s.writeJob(ctx, id, "status", w, status, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE jobs SET status=? WHERE id=?`, status, id)
		return err
	})
}

func (s *Store) AddTag(ctx context.Context, id int64, tag string, w Write) error {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" || utf8.RuneCountInString(tag) > 40 {
		return invalid("tag must be 1-40 characters")
	}
	return s.writeJob(ctx, id, "tag", w, "+"+tag, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT OR IGNORE INTO job_tags (job_id, tag) VALUES (?,?)`, id, tag)
		return err
	})
}

func (s *Store) RemoveTag(ctx context.Context, id int64, tag string, w Write) error {
	tag = strings.ToLower(strings.TrimSpace(tag))
	return s.writeJob(ctx, id, "tag", w, "-"+tag, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM job_tags WHERE job_id=? AND tag=?`, id, tag)
		return err
	})
}

func (s *Store) AddNote(ctx context.Context, id int64, note string, w Write) error {
	note = strings.TrimSpace(note)
	if note == "" || utf8.RuneCountInString(note) > 5000 {
		return invalid("note must be 1-5000 characters")
	}
	return s.writeJob(ctx, id, "note", w, "", func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO job_notes (job_id, ts, actor, note) VALUES (?,?,?,?)`, id, Now(), w.Actor, note)
		return err
	})
}

type Event struct {
	ID          int64  `json:"id"`
	TS          string `json:"ts"`
	Kind        string `json:"kind"`
	JobID       int64  `json:"job_id"`
	Actor       string `json:"actor"`
	Data        string `json:"data,omitempty"`
	Title       string `json:"title,omitempty"`
	Company     string `json:"company,omitempty"`
	CompanyName string `json:"company_name,omitempty"`
	URL         string `json:"url,omitempty"`
}

// EventsSince is the change feed: events with id > after, oldest first.
func (s *Store) EventsSince(ctx context.Context, after int64, kinds []string, limit int) ([]Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT e.id, e.ts, e.kind, COALESCE(e.job_id,0), e.actor, COALESCE(e.data,''), COALESCE(j.title,''), COALESCE(j.board,''),
		COALESCE(b.display_name,''), COALESCE(j.url,'')
		FROM events e LEFT JOIN jobs j ON j.id=e.job_id LEFT JOIN boards b ON b.ats=j.ats AND b.slug=j.board WHERE e.id > ?`
	args := []any{after}
	if len(kinds) > 0 {
		//nolint:gosec // G202: only "?" placeholders are concatenated; values are bound below
		q += ` AND e.kind IN (` + strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",") + `)`
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	q += ` ORDER BY e.id LIMIT ?`
	args = append(args, limit)
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TS, &e.Kind, &e.JobID, &e.Actor, &e.Data, &e.Title, &e.Company, &e.CompanyName, &e.URL); err != nil {
			return nil, err
		}
		if e.CompanyName == "" && e.Company != "" {
			e.CompanyName = Humanize(e.Company)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type Stats struct {
	TotalJobs      int    `json:"total_jobs"`
	OpenJobs       int    `json:"open_jobs"`
	TargetJobs     int    `json:"target_jobs"`
	NewToday       int    `json:"new_today"`
	Boards         int    `json:"boards"`
	DisabledBoards int    `json:"disabled_boards"`
	LastEventID    int64  `json:"last_event_id"`
	LastScan       string `json:"last_scan,omitempty"` // last completed sync in which at least one board succeeded
	SchemaVersion  int    `json:"schema_version"`
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(closed_at IS NULL),0), COALESCE(SUM(closed_at IS NULL AND is_target=1),0),
		COALESCE(SUM(first_seen >= ?),0) FROM jobs`, StartOfLocalDay()).Scan(&st.TotalJobs, &st.OpenJobs, &st.TargetJobs, &st.NewToday); err != nil {
		return st, err
	}
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(disabled),0) FROM boards`).Scan(&st.Boards, &st.DisabledBoards); err != nil {
		return st, err
	}
	if err := s.r.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM events`).Scan(&st.LastEventID); err != nil {
		return st, err
	}
	if err := s.r.QueryRowContext(ctx, `SELECT COALESCE(MAX(finished_at),'') FROM scan_runs WHERE boards_ok > 0`).Scan(&st.LastScan); err != nil {
		return st, err
	}
	v, err := s.SchemaVersion(ctx)
	st.SchemaVersion = v
	return st, err
}

func (s *Store) StartRun(ctx context.Context, boards int) (int64, error) {
	res, err := s.w.ExecContext(ctx, `INSERT INTO scan_runs (started_at, boards_total) VALUES (?,?)`, Now(), boards)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RunTotals are the counters recorded when a scan finishes.
type RunTotals struct{ OK, Failed, Seen, Added, Closed int }

func (s *Store) FinishRun(ctx context.Context, id int64, t RunTotals) error {
	_, err := s.w.ExecContext(ctx, `UPDATE scan_runs SET finished_at=?, boards_ok=?, boards_failed=?, jobs_seen=?, jobs_added=?, jobs_closed=? WHERE id=?`,
		Now(), t.OK, t.Failed, t.Seen, t.Added, t.Closed, id)
	return err
}

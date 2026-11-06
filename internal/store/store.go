// Package store is the SQLite datastore: one writer connection (serialized), a pool of
// read-only connections, WAL mode, and numbered embedded migrations.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNotFound is returned when a job, board or saved search does not exist.
var ErrNotFound = errors.New("not found")

// Store holds the writer and reader handles for one database file.
type Store struct {
	w    *sql.DB // exactly one connection: serializes all writes
	r    *sql.DB // read-only pool
	path string
}

// DSN builds a SQLite URI for path. Characters that are special in URIs are escaped, and
// Windows drive paths get the file:/// form.
func DSN(path string, readOnly bool) string {
	p := filepath.ToSlash(path)
	p = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(p)
	if filepath.VolumeName(path) != "" || (len(p) > 1 && p[1] == ':') {
		p = "/" + p
	}
	if strings.HasPrefix(p, "/") {
		p = "//" + p // file:///abs/path
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "synchronous(NORMAL)")
		q.Add("_txlock", "immediate")
	}
	return "file:" + p + "?" + q.Encode()
}

// Open creates the database (and parent directory) if needed and applies migrations.
func Open(path string) (*Store, error) {
	// 0700: the directory holds the user's private notes and statuses.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	w, err := sql.Open("sqlite", DSN(path, false))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if err := w.Ping(); err != nil {
		_ = w.Close()
		return nil, err
	}
	s := &Store{w: w, path: path}
	if err := s.migrate(); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	r, err := sql.Open("sqlite", DSN(path, true))
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(8)
	s.r = r
	return s, nil
}

// Path is the database file location.
func (s *Store) Path() string { return s.path }

// Close checkpoints the WAL into the main file and closes all connections.
func (s *Store) Close() error {
	var errs []error
	if s.r != nil {
		errs = append(errs, s.r.Close())
	}
	if _, err := s.w.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, s.w.Close())
	return errors.Join(errs...)
}

// SchemaVersion returns the highest applied migration.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.r.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&v)
	return v, err
}

func (s *Store) migrate() error {
	if _, err := s.w.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	var cur int
	if err := s.w.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&cur); err != nil {
		return err
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		v, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration name %q", name)
		}
		if v <= cur {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := s.inTx(context.Background(), func(tx *sql.Tx) error {
			if _, err := tx.Exec(string(body)); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			_, err := tx.Exec(`INSERT INTO schema_version VALUES (?,?)`, v, Now())
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// inTx runs fn in a write transaction, committing on success.
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Now returns the current UTC time in the store's timestamp format.
func Now() string { return time.Now().UTC().Format(timeFmt) }

const timeFmt = "2006-01-02T15:04:05Z"

// Backup writes a consistent copy of the database using VACUUM INTO.
func (s *Store) Backup(ctx context.Context, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("%s already exists", dest)
	}
	_, err := s.w.ExecContext(ctx, "VACUUM INTO ?", dest)
	return err
}

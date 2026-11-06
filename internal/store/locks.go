package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Lock is a lease held in the database, so it is visible to every process using the file.
type Lock struct {
	Name, Owner, ExpiresAt string
}

// AcquireLock takes the named lease for ttl. It returns false (and the current holder) if
// another owner holds an unexpired lease. Re-acquiring your own lease extends it.
func (s *Store) AcquireLock(ctx context.Context, name, owner string, ttl time.Duration) (bool, Lock, error) {
	var got bool
	var holder Lock
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := Now()
		if _, err := tx.Exec(`DELETE FROM locks WHERE name=? AND expires_at <= ?`, name, now); err != nil {
			return err
		}
		err := tx.QueryRow(`SELECT name, owner, expires_at FROM locks WHERE name=?`, name).Scan(&holder.Name, &holder.Owner, &holder.ExpiresAt)
		switch {
		case errors.Is(err, sql.ErrNoRows) || (err == nil && holder.Owner == owner):
			exp := time.Now().UTC().Add(ttl).Format(timeFmt)
			if _, err := tx.Exec(`INSERT INTO locks (name, owner, expires_at) VALUES (?,?,?)
				ON CONFLICT (name) DO UPDATE SET owner=excluded.owner, expires_at=excluded.expires_at`, name, owner, exp); err != nil {
				return err
			}
			got, holder = true, Lock{name, owner, exp}
			return nil
		default:
			return err
		}
	})
	return got, holder, err
}

// ReleaseLock drops the lease if owner still holds it.
func (s *Store) ReleaseLock(ctx context.Context, name, owner string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM locks WHERE name=? AND owner=?`, name, owner)
	return err
}

// LockHolder returns the current unexpired holder, if any.
func (s *Store) LockHolder(ctx context.Context, name string) (Lock, bool, error) {
	var l Lock
	err := s.r.QueryRowContext(ctx, `SELECT name, owner, expires_at FROM locks WHERE name=? AND expires_at > ?`, name, Now()).Scan(&l.Name, &l.Owner, &l.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return l, false, nil
	}
	return l, err == nil, err
}

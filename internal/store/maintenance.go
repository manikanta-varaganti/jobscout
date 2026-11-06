package store

import (
	"context"
	"database/sql"
	"time"
)

// MaintenanceReport summarises what Maintain did.
type MaintenanceReport struct {
	Compressed  int   `json:"compressed"`
	Pruned      int   `json:"pruned"`
	BytesBefore int64 `json:"bytes_before"`
	BytesAfter  int64 `json:"bytes_after"`
}

// Maintain compresses descriptions stored before compression existed, optionally drops the
// descriptions of jobs closed longer than pruneClosed (rows, history and annotations are kept),
// and optionally VACUUMs to return space to the OS.
func (s *Store) Maintain(ctx context.Context, pruneClosed time.Duration, vacuum bool) (MaintenanceReport, error) {
	var rep MaintenanceReport
	rep.BytesBefore = s.size(ctx)
	for {
		n := 0
		err := s.inTx(ctx, func(tx *sql.Tx) error {
			rows, err := tx.Query(`SELECT job_id, description FROM job_text WHERE description <> '' LIMIT 500`)
			if err != nil {
				return err
			}
			type item struct {
				id   int64
				text string
			}
			var items []item
			for rows.Next() {
				var it item
				if err := rows.Scan(&it.id, &it.text); err != nil {
					rows.Close()
					return err
				}
				items = append(items, it)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			for _, it := range items {
				if _, err := tx.Exec(`UPDATE job_text SET description='', description_z=? WHERE job_id=?`, compress(it.text), it.id); err != nil {
					return err
				}
			}
			n = len(items)
			return nil
		})
		if err != nil {
			return rep, err
		}
		rep.Compressed += n
		if n == 0 {
			break
		}
	}
	if pruneClosed > 0 {
		cutoff := time.Now().UTC().Add(-pruneClosed).Format(timeFmt)
		res, err := s.w.ExecContext(ctx, `UPDATE job_text SET description='', description_z=NULL
			WHERE job_id IN (SELECT id FROM jobs WHERE closed_at IS NOT NULL AND closed_at < ?) AND (description<>'' OR description_z IS NOT NULL)`, cutoff)
		if err != nil {
			return rep, err
		}
		n, _ := res.RowsAffected()
		rep.Pruned = int(n)
	}
	if vacuum {
		if _, err := s.w.ExecContext(ctx, `VACUUM`); err != nil {
			return rep, err
		}
	}
	rep.BytesAfter = s.size(ctx)
	return rep, nil
}

func (s *Store) size(ctx context.Context) int64 {
	var pages, size int64
	_ = s.r.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages)
	_ = s.r.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&size)
	return pages * size
}

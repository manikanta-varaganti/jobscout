package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// SavedSearch is a named search with a "new since last viewed" marker.
type SavedSearch struct {
	ID           int64        `json:"id"`
	Name         string       `json:"name"`
	Params       SearchParams `json:"params"`
	CreatedAt    string       `json:"created_at"`
	LastViewedAt string       `json:"last_viewed_at,omitempty"`
	NewCount     int          `json:"new_count"` // matches first seen after last_viewed_at
	Total        int          `json:"total"`
}

func (s *Store) ListSavedSearches(ctx context.Context, withCounts bool) ([]SavedSearch, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, name, params, created_at, COALESCE(last_viewed_at,'') FROM saved_searches ORDER BY name`)
	if err != nil {
		return nil, err
	}
	out := []SavedSearch{}
	for rows.Next() {
		var ss SavedSearch
		var raw string
		if err := rows.Scan(&ss.ID, &ss.Name, &raw, &ss.CreatedAt, &ss.LastViewedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &ss.Params); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, ss)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if withCounts {
		for i := range out {
			if err := s.countSaved(ctx, &out[i]); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func (s *Store) countSaved(ctx context.Context, ss *SavedSearch) error {
	p := ss.Params
	p.Limit = 1
	r, err := s.Search(ctx, p)
	if err != nil {
		return err
	}
	ss.Total = r.Total
	p.FirstSeenAfter = ss.LastViewedAt
	if p.FirstSeenAfter == "" {
		p.FirstSeenAfter = ss.CreatedAt
	}
	r, err = s.Search(ctx, p)
	ss.NewCount = r.Total
	return err
}

func (s *Store) GetSavedSearch(ctx context.Context, id int64) (SavedSearch, error) {
	all, err := s.ListSavedSearches(ctx, false)
	if err != nil {
		return SavedSearch{}, err
	}
	for _, ss := range all {
		if ss.ID == id {
			return ss, s.countSaved(ctx, &ss)
		}
	}
	return SavedSearch{}, ErrNotFound
}

// SaveSearch creates or replaces (by name) a saved search.
func (s *Store) SaveSearch(ctx context.Context, name string, p SearchParams) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return 0, invalid("name must be 1-80 characters")
	}
	p.Limit, p.Cursor, p.All = 0, "", false
	raw, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO saved_searches (name, params, created_at) VALUES (?,?,?)
			ON CONFLICT (name) DO UPDATE SET params=excluded.params`, name, string(raw), Now()); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT id FROM saved_searches WHERE name=?`, name).Scan(&id)
	})
	return id, err
}

func (s *Store) DeleteSavedSearch(ctx context.Context, id int64) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM saved_searches WHERE id=?`, id)
	return affected(res, err)
}

// MarkSavedSearchViewed resets the "new" counter of a saved search.
func (s *Store) MarkSavedSearchViewed(ctx context.Context, id int64) error {
	res, err := s.w.ExecContext(ctx, `UPDATE saved_searches SET last_viewed_at=? WHERE id=?`, Now(), id)
	return affected(res, err)
}

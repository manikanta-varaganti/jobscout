package httpapi

import (
	"encoding/csv"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/manikanta-varaganti/jobscout/internal/query"
	"github.com/manikanta-varaganti/jobscout/internal/store"
)

func boolQ(q url.Values, k string) bool { v := q.Get(k); return v == "1" || v == "true" }

func intQ(q url.Values, k string) int { n, _ := strconv.Atoi(q.Get(k)); return n }

// searchParams parses the shared query-string filter used by /jobs and /jobs.csv.
func searchParams(r *http.Request) store.SearchParams {
	q := r.URL.Query()
	return store.SearchParams{
		Q: q.Get("q"), Scope: query.Scope(q.Get("scope")), ATS: q.Get("ats"), Workplace: q.Get("workplace"),
		Company: q.Get("company"), Status: q.Get("status"), Location: q.Get("location"), PostedDays: intQ(q, "posted_days"),
		TargetOnly: boolQ(q, "target"), NewToday: boolQ(q, "new_today"), IncludeClosed: boolQ(q, "include_closed"),
		FirstSeenAfter: q.Get("first_seen_after"), Sort: q.Get("sort"), Group: boolQ(q, "group"),
		Limit: intQ(q, "limit"), Cursor: q.Get("cursor"),
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "invalid", "id must be an integer")
		return 0, false
	}
	return id, true
}

func (s *Server) searchJobs(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.Store.Search(r.Context(), searchParams(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, res)
}

// exportCSV streams every match (no page limit).
func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	p := searchParams(r)
	p.All, p.Cursor = true, ""
	res, err := s.svc.Store.Search(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="job-openings.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"title", "company", "ats", "location", "workplace", "department", "published_at", "first_seen", "status", "url"})
	for _, j := range res.Jobs {
		loc := j.Location
		if len(j.Locations) > 1 {
			loc = strings.Join(j.Locations, "; ")
		}
		_ = cw.Write([]string{csvSafe(j.Title), csvSafe(j.CompanyName), j.ATS, csvSafe(loc), j.Workplace, csvSafe(j.Department), j.PublishedAt, j.FirstSeen, csvSafe(j.Status), j.URL})
	}
	cw.Flush()
}

// csvSafe neutralises spreadsheet formula injection from scraped text.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	j, err := s.svc.Store.GetJob(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, j)
}

func (s *Server) setStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Status string `json:"status"`
	}
	if !ok || !decode(w, r, &body) {
		return
	}
	if err := s.svc.Store.SetStatus(r.Context(), id, body.Status, write(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) addTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Tag string `json:"tag"`
	}
	if !ok || !decode(w, r, &body) {
		return
	}
	if err := s.svc.Store.AddTag(r.Context(), id, body.Tag, write(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) removeTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.svc.Store.RemoveTag(r.Context(), id, r.PathValue("tag"), write(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) addNote(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	var body struct {
		Note string `json:"note"`
	}
	if !ok || !decode(w, r, &body) {
		return
	}
	if err := s.svc.Store.AddNote(r.Context(), id, body.Note, write(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var kinds []string
	if k := q.Get("kinds"); k != "" {
		kinds = strings.Split(k, ",")
	}
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	ev, err := s.svc.Store.EventsSince(r.Context(), after, kinds, intQ(q, "limit"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"events": ev})
}

func (s *Server) options(w http.ResponseWriter, r *http.Request) {
	ats, work, err := s.svc.Store.Options(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ats": ats, "workplace": work})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.Store.Stats(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) startScan(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.StartScan(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 202, s.svc.ScanStatus(r.Context()))
}

func (s *Server) listBoards(w http.ResponseWriter, r *http.Request) {
	b, err := s.svc.Store.ListBoards(r.Context(), true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range b {
		if b[i].DisplayName == "" {
			b[i].DisplayName = b[i].Name()
		}
	}
	writeJSON(w, 200, map[string]any{"boards": b})
}

func (s *Server) addBoard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ATS  string `json:"ats"`
		Slug string `json:"slug"`
		URL  string `json:"url"`
	}
	if !decode(w, r, &body) {
		return
	}
	var (
		ats, slug = strings.ToLower(body.ATS), body.Slug
		added     bool
		err       error
	)
	if body.URL != "" {
		ats, slug, added, err = s.svc.AddBoardURL(r.Context(), body.URL)
	} else {
		added, err = s.svc.AddBoard(r.Context(), ats, slug)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ats": ats, "slug": slug, "added": added})
}

func (s *Server) updateBoard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName *string `json:"display_name"`
		Enabled     *bool   `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	ats, slug := r.PathValue("ats"), r.PathValue("slug")
	if body.DisplayName != nil {
		if err := s.svc.Store.SetDisplayName(r.Context(), ats, slug, *body.DisplayName); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if body.Enabled != nil {
		if err := s.svc.Store.SetBoardEnabled(r.Context(), ats, slug, *body.Enabled); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) listSaved(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.Store.ListSavedSearches(r.Context(), true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"saved_searches": list})
}

func (s *Server) saveSearch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string             `json:"name"`
		Params store.SearchParams `json:"params"`
	}
	if !decode(w, r, &body) {
		return
	}
	id, err := s.svc.Store.SaveSearch(r.Context(), body.Name, body.Params)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]int64{"id": id})
}

func (s *Server) deleteSaved(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.svc.Store.DeleteSavedSearch(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) viewedSaved(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.svc.Store.MarkSavedSearchViewed(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

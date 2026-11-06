package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manikanta-varaganti/jobscout/internal/domain"
	"github.com/manikanta-varaganti/jobscout/internal/scrape"
	"github.com/manikanta-varaganti/jobscout/internal/service"
	"github.com/manikanta-varaganti/jobscout/internal/store"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddBoard(t.Context(), "lever", "acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyBoard(t.Context(), "lever", "acme", []domain.Job{
		{ExtID: "1", Title: "Backend Engineer", Location: "NYC", URL: "https://x/1", Description: "go"},
		{ExtID: "2", Title: "Backend Engineer", Location: "SF", URL: "https://x/2"},
		{ExtID: "3", Title: "=HYPERLINK(\"evil\")", URL: "https://x/3"},
	}, "2026-11-01T00:00:00Z", true); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(st, scrape.NewClient("t", 1), 1, log)
	ts := httptest.NewServer(New(svc, nil, log).Handler())
	t.Cleanup(func() {
		ts.Close()
		svc.Close()
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	return ts
}

func do(t *testing.T, method, url, body string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if host, ok := hdr["Host"]; ok {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

var csrf = map[string]string{CSRFHeader: "1"}

func TestSearchShapeAndSecurityHeaders(t *testing.T) {
	ts := newServer(t)
	resp, body := do(t, "GET", ts.URL+"/api/v1/jobs?q=backend&limit=1", "", nil)
	var res store.SearchResult
	if err := json.Unmarshal([]byte(body), &res); err != nil || resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if res.Total != 2 || res.NextCursor == "" || res.Jobs[0].CompanyName != "Acme" {
		t.Errorf("%+v", res)
	}
	_, body = do(t, "GET", ts.URL+"/api/v1/jobs?q=backend&limit=1&cursor="+res.NextCursor, "", nil)
	if !strings.Contains(body, `"total":2`) || strings.Contains(body, `"next_cursor"`) {
		t.Errorf("second page: %s", body)
	}
	_, body = do(t, "GET", ts.URL+"/api/v1/jobs?q=backend&group=1", "", nil)
	if !strings.Contains(body, `"groups":1`) || !strings.Contains(body, `"postings":2`) {
		t.Errorf("group: %s", body)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") || resp.Header.Get("X-Request-Id") == "" {
		t.Error("missing headers")
	}
	resp, body = do(t, "GET", ts.URL+"/api/v1/jobs?cursor=garbage!", "", nil)
	if resp.StatusCode != 400 || !strings.Contains(body, `"code":"bad_cursor"`) {
		t.Errorf("bad cursor: %d %s", resp.StatusCode, body)
	}
}

func TestHostGuardAndCSRF(t *testing.T) {
	ts := newServer(t)
	if resp, _ := do(t, "GET", ts.URL+"/api/v1/stats", "", map[string]string{"Host": "evil.example.com"}); resp.StatusCode != 403 {
		t.Errorf("non-loopback host got %d", resp.StatusCode)
	}
	if resp, _ := do(t, "POST", ts.URL+"/api/v1/jobs/1/status", `{"status":"applied"}`, nil); resp.StatusCode != 403 {
		t.Errorf("without header got %d", resp.StatusCode)
	}
}

func TestWritesErrorsAndIdempotency(t *testing.T) {
	ts := newServer(t)
	if resp, body := do(t, "POST", ts.URL+"/api/v1/jobs/1/status", `{"status":"applied"}`, csrf); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if _, body := do(t, "GET", ts.URL+"/api/v1/jobs?status=applied", "", nil); !strings.Contains(body, `"total":1`) {
		t.Errorf("status not applied: %s", body)
	}
	resp, body := do(t, "POST", ts.URL+"/api/v1/jobs/999/status", `{"status":"x"}`, csrf)
	if resp.StatusCode != 404 || !strings.Contains(body, `{"error":{"code":"not_found"`) {
		t.Errorf("missing job: %d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, "POST", ts.URL+"/api/v1/jobs/1/status", `{"status":"x","bogus":1}`, csrf); resp.StatusCode != 400 {
		t.Errorf("unknown field accepted: %d", resp.StatusCode)
	}
	if resp, body := do(t, "POST", ts.URL+"/api/v1/jobs/1/tags", `{"tag":""}`, csrf); resp.StatusCode != 400 || !strings.Contains(body, `"invalid"`) {
		t.Errorf("empty tag: %d %s", resp.StatusCode, body)
	}
	h := map[string]string{CSRFHeader: "1", "Idempotency-Key": "k1"}
	for i := 0; i < 3; i++ {
		do(t, "POST", ts.URL+"/api/v1/jobs/1/notes", `{"note":"hello"}`, h)
	}
	if _, body := do(t, "GET", ts.URL+"/api/v1/jobs/1", "", nil); strings.Count(body, `"note":"hello"`) != 1 {
		t.Errorf("idempotency: %s", body)
	}
	if resp, _ := do(t, "GET", ts.URL+"/api/v1/jobs/abc", "", nil); resp.StatusCode != 400 {
		t.Errorf("bad id got %d", resp.StatusCode)
	}
}

func TestCSVExportIsCompleteAndSafe(t *testing.T) {
	ts := newServer(t)
	resp, body := do(t, "GET", ts.URL+"/api/v1/jobs.csv", "", nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("%d", resp.StatusCode)
	}
	if lines := strings.Count(strings.TrimSpace(body), "\n"); lines != 3 {
		t.Errorf("want header + 3 rows, got %d newlines", lines)
	}
	if !strings.Contains(body, `'=HYPERLINK`) {
		t.Errorf("formula not neutralised: %s", body)
	}
}

func TestBoardsAndSavedSearches(t *testing.T) {
	ts := newServer(t)
	resp, body := do(t, "POST", ts.URL+"/api/v1/boards", `{"url":"https://jobs.ashbyhq.com/openai/123"}`, csrf)
	if resp.StatusCode != 200 || !strings.Contains(body, `"added":true,"ats":"ashby","slug":"openai"`) {
		t.Errorf("add by url: %d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, "POST", ts.URL+"/api/v1/boards", `{"url":"https://example.com"}`, csrf); resp.StatusCode != 400 {
		t.Errorf("bad url got %d", resp.StatusCode)
	}
	if resp, body := do(t, "PATCH", ts.URL+"/api/v1/boards/lever/acme", `{"display_name":"Acme Rockets","enabled":false}`, csrf); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if _, body := do(t, "GET", ts.URL+"/api/v1/boards", "", nil); !strings.Contains(body, `"display_name":"Acme Rockets"`) || !strings.Contains(body, `"disabled":true`) {
		t.Errorf("boards: %s", body)
	}
	if _, body := do(t, "GET", ts.URL+"/api/v1/jobs?q=company:rockets", "", nil); !strings.Contains(body, `"total":3`) {
		t.Errorf("renamed company not searchable: %s", body)
	}
	resp, body = do(t, "POST", ts.URL+"/api/v1/saved-searches", `{"name":"Backend","params":{"q":"backend","group":true}}`, csrf)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if _, body := do(t, "GET", ts.URL+"/api/v1/saved-searches", "", nil); !strings.Contains(body, `"name":"Backend"`) || !strings.Contains(body, `"total":2`) {
		t.Errorf("saved: %s", body)
	}
}

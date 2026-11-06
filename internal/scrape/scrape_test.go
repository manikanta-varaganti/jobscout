package scrape

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fastClient() *Client {
	c := NewClient("test", 4)
	c.Backoff = time.Millisecond
	return c
}

func TestHTMLToText(t *testing.T) {
	if got := HTMLToText("&lt;p&gt;Build &lt;b&gt;APIs&lt;/b&gt; &amp;amp; more&lt;/p&gt;", true); got != "Build APIs & more" {
		t.Errorf("escaped: %q", got)
	}
	if got := HTMLToText("<p>Hi</p><script>var x=1</script><style>a{}</style><li>there</li>", false); got != "Hi there" {
		t.Errorf("script: %q", got)
	}
}

func TestCanonicalURL(t *testing.T) {
	cases := map[string]string{
		"https://jobs.lever.co/acme/123?utm_source=x&lever-source=y#apply": "https://jobs.lever.co/acme/123",
		"https://careers.acme.com/jobs?gh_jid=111&gh_src=z":                "https://careers.acme.com/jobs?gh_jid=111",
		"https://boards.greenhouse.io/acme/jobs/1":                         "https://job-boards.greenhouse.io/acme/jobs/1",
		"https://jobs.lever.co/acme/1/apply":                               "https://jobs.lever.co/acme/1",
		"https://WWW.Example.com//a//b/":                                   "https://example.com/a/b",
	}
	for in, want := range cases {
		if got := CanonicalURL(in); got != want {
			t.Errorf("%s -> %s want %s", in, got, want)
		}
	}
	if CanonicalURL("https://c.com/j?gh_jid=1") == CanonicalURL("https://c.com/j?gh_jid=2") {
		t.Error("gh_jid must distinguish jobs")
	}
}

func TestLeverPaginationAndFields(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		var b []string
		skip := r.URL.Query().Get("skip")
		n := 0
		switch skip {
		case "0":
			n = 2
		case "2":
			n = 1
		}
		for i := 0; i < n; i++ {
			b = append(b, fmt.Sprintf(`{"id":"%s-%d","text":"Backend Engineer","hostedUrl":"https://jobs.lever.co/a/%s-%d","createdAt":1700000000000,
				"description":"<p>Intro</p>","lists":[{"text":"Reqs","content":"<li>Go</li>"}],"workplaceType":"Remote","categories":{"location":"NYC"}}`, skip, i, skip, i))
		}
		fmt.Fprintf(w, "[%s]", strings.Join(b, ","))
	}))
	defer srv.Close()
	l := &Lever{BaseURL: srv.URL, PageSize: 2}
	res, err := l.Fetch(context.Background(), fastClient(), "a", "")
	if err != nil || len(res.Jobs) != 3 || calls < 2 {
		t.Fatalf("jobs=%d calls=%d err=%v", len(res.Jobs), calls, err)
	}
	j := res.Jobs[0]
	if !strings.Contains(j.Description, "Go") || j.Workplace != "remote" || !strings.HasPrefix(j.PublishedAt, "2023-11-14") {
		t.Errorf("%+v", j)
	}
}

func TestGreenhouseAndAshby(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/boards/acme/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		fmt.Fprint(w, `{"jobs":[{"id":7,"title":"Software Engineer","absolute_url":"https://c.com/j?gh_jid=7","first_published":"2026-01-02T00:00:00Z",
			"location":{"name":"SF"},"departments":null,"content":"&lt;p&gt;Hello &amp;amp; bye&lt;/p&gt;"}]}`)
	})
	mux.HandleFunc("/posting-api/job-board/acme", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jobs":[{"id":"u1","title":"SWE","jobUrl":"https://jobs.ashbyhq.com/acme/u1","isRemote":true,"descriptionHtml":"<p>Hi</p>",
			"compensation":{"compensationTierSummary":"$100K"}},{"id":"","title":"skip me","jobUrl":"x"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := fastClient()
	gh := &Greenhouse{BaseURL: srv.URL}
	res, err := gh.Fetch(context.Background(), c, "acme", "")
	if err != nil || len(res.Jobs) != 1 || res.Jobs[0].Description != "Hello & bye" || res.Jobs[0].ExtID != "7" || res.ETag != `"v1"` {
		t.Fatalf("%+v %v", res, err)
	}
	res2, err := gh.Fetch(context.Background(), c, "acme", `"v1"`)
	if err != nil || !res2.NotModified {
		t.Fatalf("expected 304 handling: %+v %v", res2, err)
	}
	as := &Ashby{BaseURL: srv.URL}
	res3, err := as.Fetch(context.Background(), c, "acme", "")
	if err != nil || len(res3.Jobs) != 1 || res3.Jobs[0].Workplace != "remote" || res3.Jobs[0].Compensation != "$100K" {
		t.Fatalf("%+v %v", res3, err)
	}
}

func TestRetryAndNotFound(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone" {
			http.NotFound(w, r)
			return
		}
		if atomic.AddInt32(&n, 1) < 3 {
			http.Error(w, "boom", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	c := fastClient()
	var v map[string]any
	if _, _, err := c.GetJSON(context.Background(), srv.URL+"/x", "", &v); err != nil || n != 3 {
		t.Fatalf("retry: n=%d err=%v", n, err)
	}
	_, _, err := c.GetJSON(context.Background(), srv.URL+"/gone", "", &v)
	if !IsNotFound(err) {
		t.Errorf("want not found, got %v", err)
	}
}

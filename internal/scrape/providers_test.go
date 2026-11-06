package scrape

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestRecruitee(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/offers/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"offers":[
		 {"id":11,"title":"Backend Engineer","careers_url":"https://acme.recruitee.com/o/be?utm_source=x","location":"Amsterdam","hybrid":true,
		  "employment_type_code":"fulltime_permanent","department":"Eng","published_at":"2026-10-30 16:32:58 UTC","description":"<p>Go</p>","requirements":"<li>SQL</li>","status":"published"},
		 {"id":12,"title":"Draft","careers_url":"x","status":"draft"}]}`)
	}))
	defer srv.Close()
	r := &Recruitee{BaseURL: srv.URL + "%.0s"} // %.0s consumes the slug: the test server has no subdomains
	res, err := r.Fetch(context.Background(), fastClient(), "acme", "")
	if err != nil || len(res.Jobs) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	j := res.Jobs[0]
	if j.ExtID != "11" || j.Workplace != "hybrid" || j.Description != "Go SQL" || j.URL != "https://acme.recruitee.com/o/be" || j.EmploymentType != "fulltime permanent" {
		t.Errorf("%+v", j)
	}
	if _, err := r.Fetch(context.Background(), fastClient(), "Bad_Slug", ""); err == nil {
		t.Error("invalid subdomain accepted")
	}
}

func TestWorkable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/widget/accounts/hf" || r.URL.Query().Get("details") != "true" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"name":"Hugging Face","jobs":[{"title":"ML Engineer","shortcode":"AB12","telecommuting":true,"url":"https://apply.workable.com/j/AB12",
			"published_on":"2026-05-29","city":"Paris","state":"IDF","country":"France","description":"<p>Hi</p>"}]}`)
	}))
	defer srv.Close()
	res, err := (&Workable{BaseURL: srv.URL}).Fetch(context.Background(), fastClient(), "hf", "")
	if err != nil || res.BoardName != "Hugging Face" || len(res.Jobs) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if j := res.Jobs[0]; j.ExtID != "AB12" || j.Location != "Paris, IDF, France" || j.Workplace != "remote" || j.Description != "Hi" {
		t.Errorf("%+v", j)
	}
}

func TestSmartRecruitersPaginationAndDetail(t *testing.T) {
	const total = 5
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/postings/p3") {
			fmt.Fprint(w, `{"jobAd":{"sections":{"jobDescription":{"text":"<p>Build APIs</p>"},"qualifications":{"text":"<li>Go</li>"}}}}`)
			return
		}
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		var items []string
		for i := off; i < off+2 && i < total; i++ {
			items = append(items, fmt.Sprintf(`{"id":"p%d","name":" Engineer %d","releasedDate":"2026-11-01T00:00:00.000Z","company":{"name":"Acme Corp"},
				"location":{"fullLocation":"Austin, TX, United States","remote":true},"typeOfEmployment":{"label":"Full-time"}}`, i, i))
		}
		fmt.Fprintf(w, `{"totalFound":%d,"content":[%s]}`, total, strings.Join(items, ","))
	}))
	defer srv.Close()
	sr := &SmartRecruiters{BaseURL: srv.URL, PageSize: 2, MaxJobs: 100}
	res, err := sr.Fetch(context.Background(), fastClient(), "Acme", "")
	if err != nil || len(res.Jobs) != total || res.BoardName != "Acme Corp" {
		t.Fatalf("%d %v %s", len(res.Jobs), err, res.BoardName)
	}
	ids := map[string]bool{}
	for _, j := range res.Jobs {
		ids[j.ExtID] = true
	}
	if len(ids) != total || res.Jobs[0].Title != "Engineer 0" || res.Jobs[0].Workplace != "remote" {
		t.Errorf("%+v", res.Jobs[0])
	}
	d, err := sr.FetchDescription(context.Background(), fastClient(), "Acme", "p3")
	if err != nil || d != "Build APIs Go" {
		t.Errorf("detail %q %v", d, err)
	}
	sr.MaxJobs = 3
	if _, err := sr.Fetch(context.Background(), fastClient(), "Acme", ""); err == nil {
		t.Error("over-cap listing must fail rather than return a partial list")
	}
}

func TestParseBoardURL(t *testing.T) {
	good := map[string][2]string{
		"https://jobs.ashbyhq.com/openai/123-abc":                          {"ashby", "openai"},
		"jobs.lever.co/palantir/fb2d?lever-source=x":                       {"lever", "palantir"},
		"https://job-boards.greenhouse.io/discord/jobs/1":                  {"greenhouse", "discord"},
		"https://boards.greenhouse.io/embed/job_board?for=stripe":          {"greenhouse", "stripe"},
		"https://boards-api.greenhouse.io/v1/boards/figma/jobs":            {"greenhouse", "figma"},
		"https://bunq.recruitee.com/o/aml-manager":                         {"recruitee", "bunq"},
		"https://apply.workable.com/huggingface/":                          {"workable", "huggingface"},
		"https://jobs.smartrecruiters.com/Equinox/744000159984699-crew":    {"smartrecruiters", "Equinox"},
		"https://api.smartrecruiters.com/v1/companies/BoschGroup/postings": {"smartrecruiters", "BoschGroup"},
	}
	for in, want := range good {
		a, s, err := ParseBoardURL(in)
		if err != nil || a != want[0] || s != want[1] {
			t.Errorf("%s -> %s %s %v", in, a, s, err)
		}
	}
	for _, bad := range []string{"https://example.com/careers", "https://apply.workable.com/j/AB12", "https://jobs.lever.co/", "not a url at all"} {
		if _, _, err := ParseBoardURL(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestGreenhouseBoardName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"name":" Discord "}`) }))
	defer srv.Close()
	n, err := (&Greenhouse{BaseURL: srv.URL}).BoardName(context.Background(), fastClient(), "discord")
	if err != nil || n != "Discord" {
		t.Errorf("%q %v", n, err)
	}
}

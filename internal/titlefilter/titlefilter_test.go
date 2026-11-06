package titlefilter

import "testing"

func TestCandidate(t *testing.T) {
	yes := []string{"Software Engineer II, Data Platform", "Backend Engineer", "Back End Engineer", "Engineer, Backend",
		"Software Development Engineer II", "SDE II", "Senior Software Engineer", "Software Engineer, Internal Tools",
		"Backend Engineer, International Payments", "Smart Contract Engineer, Backend", "Software Engineer, Staffing Platform", "Full Stack Engineer"}
	no := []string{"Backend Engineering Intern", "Software Engineer Intern", "Staff Software Engineer, Backend", "Frontend Engineer",
		"Software Engineer in Test", "Engineering Manager, Backend", "Software Engineer (Contract)", "Principal Software Engineer",
		"QA Engineer", "Account Executive"}
	for _, s := range yes {
		if !Candidate(s) {
			t.Errorf("want candidate: %q", s)
		}
	}
	for _, s := range no {
		if Candidate(s) {
			t.Errorf("want reject: %q", s)
		}
	}
}

func TestMatchesFullstackNeedsBackendWords(t *testing.T) {
	if Matches("Full Stack Engineer", "We value capital and rapid growth") {
		t.Error("rapid/capital must not count as api")
	}
	if !Matches("Full Stack Engineer", "You will build REST APIs") {
		t.Error("api word should match")
	}
	if !Matches("Full Stack Software Engineer", "") {
		t.Error("explicit software engineer title should match")
	}
}

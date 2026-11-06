package query

import (
	"reflect"
	"testing"
)

const in = "(j.id IN (SELECT rowid FROM jobs_fts WHERE jobs_fts MATCH ?))"

func TestCompile(t *testing.T) {
	cases := []struct {
		q     string
		scope Scope
		sql   string
		args  []any
	}{
		{"", ScopeTitle, "", nil},
		{"   ", ScopeTitle, "", nil},
		{"AI", ScopeTitle, in, []any{`{title} : "ai"`}},
		{"agent", ScopeTitle, in, []any{`{title} : "agent"*`}},
		{"agent*", ScopeAll, in, []any{`"agent"*`}},
		{`"full stack"`, ScopeTCL, in, []any{`{title company location} : "full stack"*`}},
		{"a OR b", ScopeAll, "(" + in + " OR " + in + ")", []any{`"a"`, `"b"`}},
		{"a b", ScopeAll, "(" + in + " AND " + in + ")", []any{`"a"`, `"b"`}},
		{"-a", ScopeAll, "(NOT " + in + ")", []any{`"a"`}},
		{"NOT a", ScopeAll, "(NOT " + in + ")", []any{`"a"`}},
		{"company:Acme", ScopeTitle, in, []any{`{company} : "acme"*`}},
		{"ats:lever", ScopeTitle, "(j.ats = ? COLLATE NOCASE)", []any{"lever"}},
		{"foo:bar", ScopeAll, in, []any{`"foo:bar"*`}},
		{`say "hi"`, ScopeAll, "(" + in + " AND " + in + ")", []any{`"say"`, `"hi"`}},
		{"OR", ScopeAll, "", nil},
		{"( )", ScopeAll, "", nil},
		{"\x00", ScopeAll, "", nil},
		{"a\x00b", ScopeAll, "(" + in + " AND " + in + ")", []any{`"a"`, `"b"`}},
		{"or", ScopeAll, in, []any{`"or"`}}, // lowercase is a word, not an operator
	}
	for _, c := range cases {
		sql, args := Compile(c.q, c.scope)
		if sql != c.sql || !reflect.DeepEqual(args, c.args) {
			t.Errorf("Compile(%q, %s)\n got %s %v\nwant %s %v", c.q, c.scope, sql, args, c.sql, c.args)
		}
	}
}

func TestPrecedence(t *testing.T) {
	// AND binds tighter than OR: a OR (b AND c)
	sql, _ := Compile("a OR b c", ScopeAll)
	if want := "(" + in + " OR (" + in + " AND " + in + "))"; sql != want {
		t.Errorf("got %s", sql)
	}
}

func TestTerms(t *testing.T) {
	got := Terms(`software OR "full stack" -senior company:x`)
	if !reflect.DeepEqual(got, []string{"software", "full stack", "x"}) {
		t.Errorf("%v", got)
	}
}

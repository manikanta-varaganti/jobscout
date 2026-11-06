// Package query parses the boolean search syntax and compiles it to SQL over the FTS index.
//
// Syntax: terms are ANDed by default; OR, AND, NOT (uppercase) and | || && operators; -term negation;
// parentheses; "exact phrase"; trailing * for prefix; field:term with company, title, loc, dept, desc, ats.
// Words longer than 3 characters match as prefixes (agent matches agentic); shorter ones match whole words.
package query

import (
	"regexp"
	"strings"
	"unicode"
)

type Scope string

const (
	ScopeTitle Scope = "title"
	ScopeTCL   Scope = "tcl" // title, company, location
	ScopeAll   Scope = "all"
)

type node struct {
	op       string // and | or | not | term
	kids     []*node
	field    string // "" = use scope
	val      string
	wildcard bool
}

type tok struct {
	k        string // ( ) OR AND NOT T
	neg      bool
	field    string
	val      string
	wildcard bool
}

var tokRe = regexp.MustCompile(`(-?)(?:(\w+):)?(?:"([^"]*)"|([^\s()"]+))|([()])`)

var fieldCols = map[string]string{
	"company": "company", "co": "company", "title": "title", "loc": "location", "location": "location",
	"dept": "department", "department": "department", "team": "department", "desc": "description", "description": "description",
}

func tokenize(src string) []tok {
	// Control characters (NUL, etc.) can't appear in a meaningful query and break FTS5 strings.
	src = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, src)
	var out []tok
	for _, m := range tokRe.FindAllStringSubmatchIndex(src, -1) {
		g := func(i int) (string, bool) {
			if m[2*i] < 0 {
				return "", false
			}
			return src[m[2*i]:m[2*i+1]], true
		}
		if p, ok := g(5); ok {
			out = append(out, tok{k: p})
			continue
		}
		neg, _ := g(1)
		fld, hasField := g(2)
		q, quoted := g(3)
		bare, _ := g(4)
		val := bare
		if quoted {
			val = q
		}
		if val == "" {
			continue
		}
		if !quoted && !hasField && neg == "" {
			switch bare {
			case "OR", "|", "||":
				out = append(out, tok{k: "OR"})
				continue
			case "AND", "&&":
				out = append(out, tok{k: "AND"})
				continue
			case "NOT":
				out = append(out, tok{k: "NOT"})
				continue
			}
		}
		t := tok{k: "T", neg: neg != "", val: strings.ToLower(val)}
		if hasField {
			lf := strings.ToLower(fld)
			if _, known := fieldCols[lf]; known {
				t.field = lf
			} else if lf == "ats" {
				t.field = "ats"
			} else {
				t.val = strings.ToLower(fld + ":" + val)
			}
		}
		if strings.HasSuffix(t.val, "*") {
			t.wildcard = true
			t.val = strings.TrimSuffix(t.val, "*")
		}
		out = append(out, t)
	}
	return out
}

type parser struct {
	toks []tok
	i    int
}

func (p *parser) peek() *tok {
	if p.i < len(p.toks) {
		return &p.toks[p.i]
	}
	return nil
}

func (p *parser) or() *node {
	n := []*node{p.and()}
	for t := p.peek(); t != nil && t.k == "OR"; t = p.peek() {
		p.i++
		n = append(n, p.and())
	}
	if len(n) == 1 {
		return n[0]
	}
	return &node{op: "or", kids: n}
}

func (p *parser) and() *node {
	n := []*node{p.not()}
	for t := p.peek(); t != nil && t.k != "OR" && t.k != ")"; t = p.peek() {
		if t.k == "AND" {
			p.i++
			continue
		}
		n = append(n, p.not())
	}
	if len(n) == 1 {
		return n[0]
	}
	return &node{op: "and", kids: n}
}

func (p *parser) not() *node {
	t := p.peek()
	empty := &node{op: "and"}
	if t == nil {
		return empty
	}
	p.i++
	switch t.k {
	case "NOT":
		return &node{op: "not", kids: []*node{p.not()}}
	case "(":
		e := p.or()
		if n := p.peek(); n != nil && n.k == ")" {
			p.i++
		}
		return e
	case "T":
		if !hasWord(t.val) {
			return empty
		}
		term := &node{op: "term", field: t.field, val: t.val, wildcard: t.wildcard}
		if t.neg {
			return &node{op: "not", kids: []*node{term}}
		}
		return term
	}
	return empty // stray ) or operator
}

func hasWord(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// Compile returns a SQL boolean expression over alias j (the jobs table) and its args,
// or "" when the query is empty. It never fails: malformed input degrades gracefully
// (unbalanced parentheses close implicitly, stray operators are ignored).
func Compile(q string, scope Scope) (string, []any) {
	toks := tokenize(q)
	if len(toks) == 0 {
		return "", nil
	}
	p := &parser{toks: toks}
	root := p.or()
	var args []any
	sql := emit(root, scope, &args)
	if sql == "1" {
		return "", nil
	}
	return sql, args
}

func emit(n *node, scope Scope, args *[]any) string {
	switch n.op {
	case "and", "or":
		if len(n.kids) == 0 {
			return "1"
		}
		parts := make([]string, 0, len(n.kids))
		for _, k := range n.kids {
			parts = append(parts, emit(k, scope, args))
		}
		return "(" + strings.Join(parts, " "+strings.ToUpper(n.op)+" ") + ")"
	case "not":
		return "(NOT " + emit(n.kids[0], scope, args) + ")"
	}
	if n.field == "ats" {
		*args = append(*args, n.val)
		return "(j.ats = ? COLLATE NOCASE)"
	}
	cols := ""
	if n.field != "" {
		cols = "{" + fieldCols[n.field] + "} : "
	} else {
		switch scope {
		case ScopeTitle:
			cols = "{title} : "
		case ScopeTCL:
			cols = "{title company location} : "
		}
	}
	phrase := `"` + strings.ReplaceAll(n.val, `"`, `""`) + `"`
	if n.wildcard || len([]rune(n.val)) > 3 {
		phrase += "*"
	}
	*args = append(*args, cols+phrase)
	return "(j.id IN (SELECT rowid FROM jobs_fts WHERE jobs_fts MATCH ?))"
}

// Terms returns the positive words in the query, for highlighting.
func Terms(q string) []string {
	var out []string
	for _, t := range tokenize(q) {
		if t.k == "T" && !t.neg && hasWord(t.val) {
			out = append(out, t.val)
		}
	}
	return out
}

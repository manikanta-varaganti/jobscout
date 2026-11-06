package scrape

import (
	"html"
	"strings"

	xhtml "golang.org/x/net/html"
)

// HTMLToText strips tags and returns whitespace-collapsed text. When escaped is true the input is
// entity-escaped HTML (Greenhouse) and is unescaped once before parsing. script/style are dropped.
func HTMLToText(s string, escaped bool) string {
	if s == "" {
		return ""
	}
	if escaped {
		s = html.UnescapeString(s)
	}
	z := xhtml.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := 0
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return strings.Join(strings.Fields(b.String()), " ")
		case xhtml.StartTagToken:
			if n, _ := z.TagName(); isSkipped(n) {
				skip++
			}
			b.WriteByte(' ')
		case xhtml.EndTagToken:
			if n, _ := z.TagName(); isSkipped(n) && skip > 0 {
				skip--
			}
			b.WriteByte(' ')
		case xhtml.SelfClosingTagToken:
			b.WriteByte(' ')
		case xhtml.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		}
	}
}

func isSkipped(name []byte) bool {
	n := string(name)
	return n == "script" || n == "style"
}

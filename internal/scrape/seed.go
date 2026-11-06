package scrape

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"
)

//go:embed boards.seed.json
var seedJSON []byte

// BoardRef identifies a board on an ATS.
type BoardRef struct {
	ATS  string `json:"ats"`
	Slug string `json:"slug"`
}

var slugRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// ValidSlug reports whether s is a plausible board slug (guards URL construction).
func ValidSlug(s string) bool { return slugRe.MatchString(s) }

// SeedBoards returns the starter list of boards shipped with the binary.
func SeedBoards() ([]BoardRef, error) {
	var out []BoardRef
	if err := json.Unmarshal(seedJSON, &out); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].ATS = strings.ToLower(out[i].ATS)
	}
	return out, nil
}

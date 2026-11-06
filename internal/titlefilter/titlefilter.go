// Package titlefilter decides whether a posting looks like a software / backend engineering role.
package titlefilter

import (
	"regexp"
	"strings"
)

var (
	excluded = regexp.MustCompile(`\b(intern|internship|contractor|qa|quality assurance|manager|director|vice president|vp|principal|sdet|test)\b` +
		`|\bstaff\b|\bfront[- ]?end\b|\((?:contract|temp(?:orary)?)\)|[-,–] ?contract$|\bcontract (?:to hire|position|role)\b`)
	role = regexp.MustCompile(`\bsoftware (?:development )?(?:engineer|developer)\b|\b(?:sde|swe)\b` +
		`|\bback[- ]?end(?: software)? (?:engineer|developer)\b|\b(?:engineer|developer)\W+back[- ]?end\b`)
	fullstack      = regexp.MustCompile(`\bfull[- ]?stack\b.*\b(?:engineer|developer)\b|\b(?:engineer|developer)\b.*\bfull[- ]?stack\b`)
	fullstackTitle = regexp.MustCompile(`\bfull[- ]?stack\b`)
	backendHint    = regexp.MustCompile(`\b(?:back[- ]?end|server[- ]side|apis?|distributed|platform)\b`)
	spaces         = regexp.MustCompile(`\s+`)
)

func norm(t string) string {
	t = strings.ToLower(strings.TrimSpace(spaces.ReplaceAllString(t, " ")))
	// "Member of Technical Staff" is not seniority; keep it out of the \bstaff\b exclusion.
	return strings.ReplaceAll(t, "technical staff", "technical-team")
}

// Candidate reports whether the title alone looks like a target role.
func Candidate(title string) bool {
	t := norm(title)
	if t == "" || excluded.MatchString(t) {
		return false
	}
	return role.MatchString(t) || fullstack.MatchString(t)
}

// Matches also checks the description for ambiguous "full stack" titles.
func Matches(title, description string) bool {
	if !Candidate(title) {
		return false
	}
	t := norm(title)
	if fullstackTitle.MatchString(t) && !role.MatchString(t) {
		return backendHint.MatchString(strings.ToLower(description))
	}
	return true
}

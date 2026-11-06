// Package domain holds the types shared by every layer. It imports nothing from the project.
package domain

// Job is a posting as scraped or imported. Description is plain text.
type Job struct {
	ATS, Board, ExtID                                string
	Title, Location, Workplace, EmploymentType       string
	Department, Team, Compensation, URL, PublishedAt string
	Description                                      string
	FirstSeen                                        string // optional override, used by the importer
}

// Package config resolves runtime settings from flags' defaults and environment variables.
package config

import (
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Home      string // data directory
	DBPath    string
	UserAgent string
	Workers   int // boards scanned concurrently
	PerHost   int // concurrent requests per ATS host
}

// Load reads JOBSCOUT_HOME, JOBSCOUT_DB, JOBSCOUT_USER_AGENT, JOBSCOUT_WORKERS, JOBSCOUT_PER_HOST.
// The default home is the per-user config directory: ~/Library/Application Support/jobscout on macOS,
// %AppData%\jobscout on Windows, ~/.config/jobscout on Linux. It is never inside the install location.
func Load() (Config, error) {
	c := Config{
		UserAgent: envOr("JOBSCOUT_USER_AGENT", "jobscout (+https://github.com/manikanta-varaganti/jobscout; job board inventory)"),
		Workers:   envInt("JOBSCOUT_WORKERS", 8),
		PerHost:   envInt("JOBSCOUT_PER_HOST", 6),
	}
	c.Home = os.Getenv("JOBSCOUT_HOME")
	if c.Home == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return c, err
		}
		c.Home = filepath.Join(base, "jobscout")
	}
	c.DBPath = envOr("JOBSCOUT_DB", filepath.Join(c.Home, "jobscout.db"))
	return c, nil
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envInt(k string, d int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil && n > 0 {
		return n
	}
	return d
}

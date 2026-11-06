package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultsAndOverrides(t *testing.T) {
	t.Setenv("JOBSCOUT_HOME", "")
	t.Setenv("JOBSCOUT_DB", "")
	t.Setenv("JOBSCOUT_WORKERS", "not-a-number")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(c.Home) != "jobscout" || c.DBPath != filepath.Join(c.Home, "jobscout.db") || c.Workers != 8 || c.PerHost != 6 {
		t.Errorf("defaults: %+v", c)
	}
	home := t.TempDir()
	t.Setenv("JOBSCOUT_HOME", home)
	t.Setenv("JOBSCOUT_WORKERS", "3")
	if c, _ = Load(); c.DBPath != filepath.Join(home, "jobscout.db") || c.Workers != 3 {
		t.Errorf("overrides: %+v", c)
	}
	t.Setenv("JOBSCOUT_DB", filepath.Join(home, "other.db"))
	if c, _ = Load(); c.DBPath != filepath.Join(home, "other.db") {
		t.Errorf("db override: %+v", c)
	}
}

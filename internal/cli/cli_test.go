package cli

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// runCLI runs the CLI against an isolated data directory; paths are normalised to $HOME.
func runCLI(t *testing.T, home string, args ...string) (string, string, int) {
	t.Helper()
	t.Setenv("JOBSCOUT_HOME", home)
	t.Setenv("JOBSCOUT_DB", "")
	var out, errb bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &out, &errb, "test")
	norm := func(s string) string { return strings.ReplaceAll(s, home, "$HOME") }
	return norm(out.String()), norm(errb.String()), code
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test ./internal/cli -update`)", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestGoldenWorkflow(t *testing.T) {
	home := t.TempDir()
	var transcript strings.Builder
	step := func(args ...string) {
		out, errs, code := runCLI(t, home, args...)
		transcript.WriteString("$ jobscout " + strings.Join(args, " ") + "\n" + out)
		if errs != "" {
			transcript.WriteString("[stderr] " + errs)
		}
		transcript.WriteString("[exit " + string(rune('0'+code)) + "]\n\n")
	}
	step("version")
	step("board", "add", "lever", "acme")
	step("board", "add-url", "https://jobs.ashbyhq.com/openai/123")
	step("board", "add-url", "https://jobs.ashbyhq.com/openai")
	step("board", "rename", "lever", "acme", "Acme Rockets")
	step("board", "disable", "lever", "acme")
	step("board", "list", "--all")
	step("saved", "add", "Backend", "backend OR \"full stack\"", "--target")
	step("saved", "list")
	step("search", "backend", "--limit", "5")
	step("board", "nonsense")
	step("nope")
	golden(t, "workflow", transcript.String())
}

func TestUsageAndHelp(t *testing.T) {
	out, _, code := runCLI(t, t.TempDir(), "help")
	if code != 0 || !strings.Contains(out, "Commands:") || !strings.Contains(out, "double-clicking") {
		t.Errorf("usage: %d %s", code, out)
	}
	if _, _, code := runCLI(t, t.TempDir(), "search", "-h"); code != 0 {
		t.Errorf("-h exit %d", code)
	}
}

func TestParseDays(t *testing.T) {
	if d, err := parseDays("90d"); err != nil || d.Hours() != 90*24 {
		t.Error(d, err)
	}
	if _, err := parseDays("x d"); err == nil {
		t.Error("accepted garbage")
	}
}

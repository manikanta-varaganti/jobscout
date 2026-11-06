// Command jobscout is a local job-postings inventory: scan public ATS boards, search them, and let agents use them.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/manikanta-varaganti/jobscout/internal/cli"
)

// Set at build time: -ldflags "-X main.version=v1.2.3".
var version = ""

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, resolveVersion())
	stop()
	os.Exit(code)
}

// resolveVersion prefers the release ldflag, then module info (go install), then VCS info.
func resolveVersion() string {
	if version != "" {
		return version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	rev, dirty := "", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if rev == "" {
		return "dev"
	}
	if dirty {
		rev += "-dirty"
	}
	return "dev-" + rev
}

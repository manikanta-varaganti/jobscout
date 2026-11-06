package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultAddr    = "127.0.0.1:8765"
	launchInterval = 6 * time.Hour
)

const banner = `
  jobscout is starting…

    Your dashboard: %s  (it opens in your browser)
    Keep this window open while you use jobscout. Close it to stop.
    New jobs are fetched every 6 hours while it runs, or click "Sync now".

`

// isInteractive reports whether stdin is a terminal (a person can press Enter). A variable for tests.
var isInteractive = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// launch runs when jobscout is started with no arguments, which is what double-clicking it does.
// It opens an already-running jobscout if there is one; otherwise it starts the dashboard with
// automatic syncing. On failure it keeps the window open so the message can be read.
func launch(e *env) int {
	addr := os.Getenv("JOBSCOUT_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	if !isLoopback(addr) {
		fmt.Fprintf(e.err, "error: JOBSCOUT_ADDR must be a loopback address such as 127.0.0.1:8765 (got %q)\n", addr)
		e.pause()
		return 2
	}
	url := "http://" + addr + "/"
	if runningAt(e.ctx, addr) {
		fmt.Fprintf(e.out, "jobscout is already running. Opening %s in your browser.\n", url)
		openBrowser(url)
		return 0
	}
	fmt.Fprintf(e.out, banner, url)
	err := cmdServe(e, []string{"--addr", addr, "--scan-every", launchInterval.String(), "--sync-if-older", launchInterval.String()})
	if err == nil {
		return 0
	}
	msg := err.Error()
	if portInUse(err) {
		msg = fmt.Sprintf("another program is already using %s, so jobscout can't start there.\n"+
			"Close that program, or start jobscout on another port from a terminal:\n    jobscout serve --addr 127.0.0.1:8800", addr)
	}
	fmt.Fprintln(e.err, "\nerror:", msg)
	e.pause()
	return 1
}

func portInUse(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "address already in use") || strings.Contains(m, "only one usage of each socket address")
}

// isLoopback reports whether addr is host:port on this machine only.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// runningAt reports whether a jobscout dashboard is already answering at addr.
func runningAt(ctx context.Context, addr string) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if !isLoopback(addr) {
		return false
	}
	// addr is verified loopback above, so this request can only reach this machine.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil) //nolint:gosec // G704: loopback only
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // G704: loopback only
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var h struct {
		App string `json:"app"`
	}
	return resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&h) == nil && h.App == "jobscout"
}

// pause waits for Enter when a person is watching, so a double-clicked window doesn't vanish
// before the message can be read.
func (e *env) pause() {
	if !e.interactive {
		return
	}
	fmt.Fprint(e.err, "\nPress Enter to close this window.")
	_, _ = bufio.NewReader(e.in).ReadString('\n')
}

package cli

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manikanta-varaganti/jobscout/internal/store"
)

// stubBrowser records URLs instead of opening a browser.
func stubBrowser(t *testing.T) chan string {
	t.Helper()
	opened := make(chan string, 4)
	prev := openBrowser
	openBrowser = func(u string) { opened <- u }
	t.Cleanup(func() { openBrowser = prev })
	return opened
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func TestLaunchOpensAlreadyRunningInstance(t *testing.T) {
	opened := stubBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok","app":"jobscout"}`)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("JOBSCOUT_ADDR", addr)
	out, _, code := runCLI(t, t.TempDir())
	if code != 0 || !strings.Contains(out, "already running") {
		t.Fatalf("%d %q", code, out)
	}
	if u := <-opened; u != "http://"+addr+"/" {
		t.Errorf("opened %s", u)
	}
}

func TestLaunchStartsDashboardAndStopsCleanly(t *testing.T) {
	opened := stubBrowser(t)
	home := t.TempDir()
	// Pretend a sync just happened so the test never touches the network.
	st, err := store.Open(filepath.Join(home, "jobscout.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.StartRun(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishRun(context.Background(), id, store.RunTotals{OK: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	addr := freeAddr(t)
	t.Setenv("JOBSCOUT_HOME", home)
	t.Setenv("JOBSCOUT_DB", "")
	t.Setenv("JOBSCOUT_ADDR", addr)

	ctx, cancel := context.WithCancel(context.Background())
	var out, errb bytes.Buffer
	codec := make(chan int, 1)
	go func() { codec <- Run(ctx, nil, strings.NewReader(""), &out, &errb, "test") }()

	deadline := time.Now().Add(10 * time.Second)
	for !runningAt(context.Background(), addr) {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("dashboard never came up: %s", errb.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case u := <-opened:
		if u != "http://"+addr+"/" {
			t.Errorf("opened %s", u)
		}
	case <-time.After(5 * time.Second):
		t.Error("browser was not opened")
	}
	cancel() // what Ctrl+C or closing the window does
	select {
	case code := <-codec:
		if code != 0 {
			t.Errorf("exit %d: %s", code, errb.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not stop after cancel")
	}
	if !strings.Contains(out.String(), "Keep this window open") || strings.Contains(out.String(), "Fetching the latest jobs") {
		t.Errorf("banner / unexpected start-up sync: %s", out.String())
	}
}

func TestLaunchPortTakenShowsHelpAndWaits(t *testing.T) {
	stubBrowser(t)
	other := httptest.NewServer(http.NotFoundHandler()) // some other program on the port
	defer other.Close()
	t.Setenv("JOBSCOUT_ADDR", strings.TrimPrefix(other.URL, "http://"))
	t.Setenv("JOBSCOUT_HOME", t.TempDir())
	t.Setenv("JOBSCOUT_DB", "")
	prev := isInteractive
	isInteractive = func(io.Reader) bool { return true }
	t.Cleanup(func() { isInteractive = prev })

	var out, errb bytes.Buffer
	code := Run(context.Background(), nil, strings.NewReader("\n"), &out, &errb, "test")
	if code != 1 || !strings.Contains(errb.String(), "another program is already using") || !strings.Contains(errb.String(), "Press Enter to close") {
		t.Errorf("%d %s", code, errb.String())
	}
}

func TestSyncDue(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	cases := map[string]bool{"": true, "garbage": true, "2026-11-06T11:00:00Z": false, "2026-11-06T05:00:00Z": true, "2026-11-06T06:00:01Z": false}
	for last, want := range cases {
		if got := syncDue(last, 6*time.Hour, now); got != want {
			t.Errorf("syncDue(%q) = %v", last, got)
		}
	}
}

func TestLaunchRejectsNonLoopbackAddr(t *testing.T) {
	stubBrowser(t)
	t.Setenv("JOBSCOUT_ADDR", "0.0.0.0:8765")
	_, errs, code := runCLI(t, t.TempDir())
	if code != 2 || !strings.Contains(errs, "loopback") {
		t.Errorf("%d %s", code, errs)
	}
	for addr, want := range map[string]bool{"127.0.0.1:1": true, "localhost:1": true, "[::1]:1": true, "192.168.1.5:1": false, "example.com:80": false, "nonsense": false} {
		if isLoopback(addr) != want {
			t.Errorf("isLoopback(%q) != %v", addr, want)
		}
	}
}

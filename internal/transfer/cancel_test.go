package transfer

import (
	"bufio"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/rjayasin/rtr/internal/config"
)

// fakeRsync writes a fake "rsync" shell script into dir that forks a worker
// appending to marker, prints one progress line, and waits on the worker.
func fakeRsync(t *testing.T, dir, marker string) string {
	t.Helper()
	script := filepath.Join(dir, "fake-rsync.sh")
	body := "#!/bin/sh\n" +
		"( while true; do echo tick >> \"" + marker + "\"; sleep 0.1; done ) &\n" +
		"echo \"  0   0%    0.00kB/s    0:00:00\"\n" +
		"wait\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// drainUntilDone consumes events until the terminal Done (or channel close),
// returning the last Done event seen.
func drainUntilDone(t *testing.T, ch <-chan Event, timeout time.Duration) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return Event{Done: true}
			}
			if ev.Done {
				return ev
			}
		case <-deadline:
			t.Fatal("transfer did not finish within the deadline")
		}
	}
}

// Killing a transfer must stop rsync's forked children too, not just the main
// process. A fake "rsync" forks a worker that keeps appending to a file and the
// parent waits on it. With Setsid, pid == pgid, so Handle.Kill's group kill
// takes down the worker as well.
func TestKillStopsChildProcesses(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "alive")
	script := fakeRsync(t, dir, marker)

	h, err := StartDetached(Job{
		Bookmark:  config.Bookmark{Host: "h", User: "u"},
		Sources:   []string{"/x"},
		LocalDest: dir,
		Cfg:       config.RsyncConfig{Binary: script},
	}, filepath.Join(dir, "xfer.log"))
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(400 * time.Millisecond) // let the worker append a few lines
	if err := h.Kill(); err != nil {   // like pressing `c` in the transfers panel
		t.Fatal(err)
	}
	drainUntilDone(t, h.Events, 5*time.Second)

	before := countLines(t, marker)
	time.Sleep(400 * time.Millisecond)
	after := countLines(t, marker)
	if after != before {
		t.Errorf("worker kept running after kill: %d -> %d lines", before, after)
	}
}

// The detached process must be its own session/process-group leader so it
// survives rtr exiting and group-kill semantics hold.
func TestStartDetachedOwnSession(t *testing.T) {
	dir := t.TempDir()
	script := fakeRsync(t, dir, filepath.Join(dir, "alive"))

	h, err := StartDetached(Job{
		Bookmark:  config.Bookmark{Host: "h", User: "u"},
		Sources:   []string{"/x"},
		LocalDest: dir,
		Cfg:       config.RsyncConfig{Binary: script},
	}, filepath.Join(dir, "xfer.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Kill()

	pgid, err := syscall.Getpgid(h.PID)
	if err != nil {
		t.Fatal(err)
	}
	if pgid != h.PID {
		t.Errorf("detached process is not its own group leader: pid=%d pgid=%d", h.PID, pgid)
	}
	if !Alive(h.PID, script) {
		t.Error("Alive() = false for a running detached transfer")
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		n++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return n
}

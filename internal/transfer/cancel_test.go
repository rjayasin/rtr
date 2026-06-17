package transfer

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rjayasin/rtr/internal/config"
)

// Cancelling a transfer must stop rsync's forked children too, not just the main
// process. A fake "rsync" forks a worker that keeps appending to a file and the
// parent waits on it (also holding the output pipe). Under the old single-process
// kill the worker would keep running (and Wait would block on the open pipe);
// with process-group kill, everything stops.
func TestStartCancelStopsChildProcesses(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "alive")
	script := filepath.Join(dir, "fake-rsync.sh")
	body := "#!/bin/sh\n" +
		"( while true; do echo tick >> \"" + marker + "\"; sleep 0.1; done ) &\n" +
		"echo \"  0   0%    0.00kB/s    0:00:00\"\n" +
		"wait\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := Start(ctx, Job{
		Bookmark:  config.Bookmark{Host: "h", User: "u"},
		Sources:   []string{"/x"},
		LocalDest: dir,
		Cfg:       config.RsyncConfig{Binary: script},
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(400 * time.Millisecond) // let the worker append a few lines
	cancel()                           // like pressing `c` in the transfers panel

	deadline := time.After(5 * time.Second)
	for done := false; !done; {
		select {
		case ev, ok := <-ch:
			if !ok || ev.Done {
				done = true
			}
		case <-deadline:
			t.Fatal("transfer did not stop within 5s after cancel (children survived?)")
		}
	}

	before := countLines(t, marker)
	time.Sleep(400 * time.Millisecond)
	after := countLines(t, marker)
	if after != before {
		t.Errorf("worker kept running after cancel: %d -> %d lines", before, after)
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

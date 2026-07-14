package transfer

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// shorten speeds up the tail/liveness polling for the duration of a test.
func shorten(t *testing.T) {
	t.Helper()
	oldTail, oldLive := tailPoll, livenessPoll
	tailPoll, livenessPoll = 10*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { tailPoll, livenessPoll = oldTail, oldLive })
}

// Attach must replay existing log content as coalesced events (one Progress,
// one Line), stream new output live, and report Done{ErrExitUnknown} when the
// process dies.
func TestAttachReplaysAndDetectsDeath(t *testing.T) {
	shorten(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "xfer.log")

	// History: several progress samples and a diagnostic line, \r-separated like
	// rsync's in-place updates, plus a trailing partial line the tailer must not
	// split.
	history := "sending incremental file list\n" +
		"  1,000   1%    9.99MB/s    0:01:00\r" +
		"  2,000   2%    9.99MB/s    0:00:59\r" +
		"  3,00" // partial
	if err := os.WriteFile(logPath, []byte(history), 0o600); err != nil {
		t.Fatal(err)
	}

	// A stand-in for the still-running rsync: sleeps until killed. It must be
	// reaped promptly after the kill — a zombie still answers kill(pid, 0),
	// whereas a real detached transfer is reaped by init the moment it dies.
	proc := exec.Command("sleep", "60")
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	killProc := func() {
		proc.Process.Kill()
		proc.Wait()
	}
	defer killProc()

	h, err := Attach(proc.Process.Pid, logPath)
	if err != nil {
		t.Fatal(err)
	}

	var got []Event
	timeout := time.After(5 * time.Second)
	expect := func(what string, ok func(Event) bool) Event {
		t.Helper()
		for {
			select {
			case ev := <-h.Events:
				got = append(got, ev)
				if ok(ev) {
					return ev
				}
				t.Fatalf("expecting %s, got %+v (after %+v)", what, ev, got)
			case <-timeout:
				t.Fatalf("timed out expecting %s (after %+v)", what, got)
			}
		}
	}

	expect("coalesced line", func(ev Event) bool { return ev.Line == "sending incremental file list" })
	ev := expect("coalesced progress", func(ev Event) bool { return ev.Progress != nil })
	if ev.Progress.Percent != 2 {
		t.Errorf("coalesced progress percent = %v, want 2 (the last sample)", ev.Progress.Percent)
	}

	// Live tail: complete the partial line and append a new sample.
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("0   3%    9.99MB/s    0:00:58\r  4,000   4%    9.99MB/s    0:00:57\r"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ev = expect("live progress 3%", func(ev Event) bool { return ev.Progress != nil })
	if ev.Progress.Percent != 3 {
		t.Errorf("first live sample percent = %v, want 3 (partial line joined)", ev.Progress.Percent)
	}
	expect("live progress 4%", func(ev Event) bool { return ev.Progress != nil && ev.Progress.Percent == 4 })

	// Kill the process: the tailer must notice and emit Done{ErrExitUnknown}.
	killProc()
	ev = expect("done", func(ev Event) bool { return ev.Done })
	if ev.Err != ErrExitUnknown {
		t.Errorf("Done.Err = %v, want ErrExitUnknown", ev.Err)
	}
	if _, ok := <-h.Events; ok {
		t.Error("Events not closed after Done")
	}
}

func TestAttachMissingLog(t *testing.T) {
	if _, err := Attach(os.Getpid(), filepath.Join(t.TempDir(), "nope.log")); err == nil {
		t.Fatal("Attach with a missing log should error so the caller respawns")
	}
}

func TestAliveDeadOrForeignPid(t *testing.T) {
	// A freshly exited process: pid is gone.
	proc := exec.Command("true")
	if err := proc.Run(); err != nil {
		t.Fatal(err)
	}
	if Alive(proc.ProcessState.Pid(), "rsync") {
		t.Error("Alive() = true for an exited pid")
	}
	if Alive(0, "rsync") || Alive(-1, "rsync") {
		t.Error("Alive() = true for pid <= 0")
	}
	// The test binary itself is alive but is not rsync (and is not a group
	// leader in the normal `go test` setup).
	if Alive(os.Getpid(), "rsync") {
		t.Error("Alive() = true for a non-rsync process")
	}
}

// tailReader must deliver appended bytes, then EOF only after done() with a
// final drain of anything written just before death.
func TestTailReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte("early"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	done := false
	tr := &tailReader{f: f, poll: time.Millisecond, done: func() bool { return done }}

	buf := make([]byte, 16)
	n, err := tr.Read(buf)
	if err != nil || string(buf[:n]) != "early" {
		t.Fatalf("first read = %q, %v", buf[:n], err)
	}

	// Append while "running", then flip done and append the dying words.
	appendTo := func(s string) {
		w, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()
		if _, err := w.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	appendTo("mid")
	n, err = tr.Read(buf)
	if err != nil || string(buf[:n]) != "mid" {
		t.Fatalf("second read = %q, %v", buf[:n], err)
	}

	done = true
	appendTo("last")
	rest, err := io.ReadAll(tr)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "last" {
		t.Fatalf("drain after done = %q, want %q", rest, "last")
	}
}

package transfer

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrExitUnknown is the Done.Err reported for an attached process whose exit
// status could not be observed (it was not our child). The caller should
// respawn once with --partial to finish and verify the transfer.
var ErrExitUnknown = errors.New("transfer: process ended; exit status unknown")

// Poll cadences, as variables so tests can shorten them.
var (
	tailPoll     = 200 * time.Millisecond // log-file tail when no new bytes
	livenessPoll = time.Second            // attached-pid existence check
)

// Handle is one running rsync process, detached from rtr so it survives rtr
// exiting. Progress is read by tailing LogPath; Events carries the parsed
// stream and is closed after the final Done event.
type Handle struct {
	PID     int
	LogPath string
	Events  <-chan Event
}

// Kill SIGKILLs the whole process group. rsync is spawned with Setsid, so its
// pid is also its pgid and this takes down rsync's forked workers and ssh too.
// A process that is already gone is not an error.
func (h *Handle) Kill() error {
	err := syscall.Kill(-h.PID, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

// StartDetached launches rsync in its own session with stdout+stderr written
// to logPath, so the process is immune to rtr exiting and its output survives
// for a later Attach. The returned handle streams progress by tailing the log;
// Events ends with Done{Err: <real exit status>} since the process is our
// child. The log file is truncated: each spawn owns a fresh log.
func StartDetached(j Job, logPath string) (*Handle, error) {
	bin := j.Cfg.Binary
	if bin == "" {
		bin = "rsync"
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	logW, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	logR, err := os.Open(logPath)
	if err != nil {
		logW.Close()
		return nil, err
	}

	cmd := exec.Command(bin, BuildArgs(j)...)
	// Setsid detaches rsync into its own session: it keeps running when rtr
	// exits (reparenting to init) and, as session leader, pid == pgid, so
	// killing -pid still takes down the workers rsync forks and its ssh
	// transport. Both output streams go to the log file — rsync writes progress
	// to stdout and diagnostics to stderr, and the UI log shows errors inline.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = logW
	cmd.Stderr = logW

	if err := cmd.Start(); err != nil {
		logW.Close()
		logR.Close()
		return nil, err
	}
	logW.Close() // the child owns its own copy of the fd now

	ch := make(chan Event, 64)
	h := &Handle{PID: cmd.Process.Pid, LogPath: logPath, Events: ch}

	// Wait in a goroutine so the child is reaped while rtr lives and its real
	// exit status is captured; if rtr exits first the child simply reparents.
	exited := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(exited)
	}()

	poll := tailPoll // snapshot: tests shorten the package var
	go func() {
		defer close(ch)
		defer logR.Close()
		scanErr := pump(&tailReader{f: logR, poll: poll, done: closed(exited)}, ch)
		<-exited
		err := waitErr
		if err == nil {
			err = scanErr // surface a read error only if the process itself succeeded
		}
		ch <- Event{Done: true, Err: err}
	}()
	return h, nil
}

// Attach reconnects to a detached rsync left running by a previous rtr run. It
// scans the existing log synchronously, coalescing it into at most one Line
// and one Progress event (so a long history never floods the UI's
// one-event-per-frame pump), then live-tails the log while polling pid
// liveness. Events ends with Done{Err: ErrExitUnknown} when the pid dies —
// the process is not our child, so its exit status is unobservable and the
// caller should respawn once to finish/verify. A missing or unreadable log is
// returned as an error so the caller can fall back to a fresh spawn.
func Attach(pid int, logPath string) (*Handle, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		f.Close()
		return nil, err
	}

	// Catch-up scan. Only complete (\r/\n-terminated) tokens are consumed; a
	// trailing partial line is left for the tailer so it is never split in two.
	var lastProg *Progress
	var lastLine string
	offset, start := 0, 0
	for i, b := range data {
		if b != '\n' && b != '\r' {
			continue
		}
		tok := strings.TrimRight(string(data[start:i]), " ")
		start, offset = i+1, i+1
		if tok == "" {
			continue
		}
		if p, ok := ParseProgressLine(tok); ok {
			pc := p
			lastProg = &pc
		} else {
			lastLine = tok
		}
	}
	if _, err := f.Seek(int64(offset), io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}

	ch := make(chan Event, 64)
	if lastLine != "" {
		ch <- Event{Line: lastLine}
	}
	if lastProg != nil {
		ch <- Event{Progress: lastProg}
	}

	tail, live := tailPoll, livenessPoll // snapshot: tests shorten the package vars
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		for syscall.Kill(pid, 0) == nil {
			time.Sleep(live)
		}
	}()

	go func() {
		defer close(ch)
		defer f.Close()
		pump(&tailReader{f: f, poll: tail, done: closed(exited)}, ch)
		ch <- Event{Done: true, Err: ErrExitUnknown}
	}()
	return &Handle{PID: pid, LogPath: logPath, Events: ch}, nil
}

// Alive reports whether pid still looks like the detached rsync we spawned.
// A bare kill(pid, 0) is not enough: the pid may have been reused since the
// journal was written, and a false positive would let cancel SIGKILL an
// innocent process group. Layered checks, any failure → treat as dead (the
// fallback respawn is always safe): the pid exists, it is its own process
// group leader (guaranteed by Setsid at spawn), and ps names the expected
// binary.
func Alive(pid int, binary string) bool {
	if pid <= 0 {
		return false
	}
	if syscall.Kill(pid, 0) != nil {
		return false // ESRCH: gone; EPERM: exists but not ours
	}
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
		return false
	}
	base := "rsync"
	if binary != "" {
		base = filepath.Base(binary)
	}
	// comm is the executable; for interpreted fakes (tests) it may be the shell,
	// so fall back to the full argv.
	for _, col := range []string{"comm=", "args="} {
		out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", col).Output()
		if err != nil {
			continue
		}
		s := string(out)
		if strings.Contains(s, base) || strings.Contains(s, "rsync") {
			return true
		}
	}
	return false
}

// pump scans rsync output from r (split on \r and \n so in-place progress
// updates surface as individual samples), emitting Progress and Line events on
// ch until the reader is exhausted. It does not send Done or close ch; it
// returns the scanner error, if any.
func pump(r io.Reader, ch chan<- Event) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	sc.Split(scanCRLF)
	for sc.Scan() {
		tok := strings.TrimRight(sc.Text(), " ")
		if tok == "" {
			continue
		}
		if p, ok := ParseProgressLine(tok); ok {
			pc := p
			ch <- Event{Progress: &pc}
		} else {
			ch <- Event{Line: tok}
		}
	}
	return sc.Err()
}

// tailReader turns an append-only log file into a blocking stream: on EOF it
// polls until done() reports the writer has exited, then drains once more (to
// catch bytes written just before death) and returns io.EOF.
type tailReader struct {
	f    *os.File
	poll time.Duration
	done func() bool
}

func (t *tailReader) Read(p []byte) (int, error) {
	for {
		n, err := t.f.Read(p)
		if n > 0 {
			return n, nil
		}
		if err != nil && err != io.EOF {
			return 0, err
		}
		if t.done() {
			n, err := t.f.Read(p)
			if n > 0 {
				return n, nil
			}
			if err != nil && err != io.EOF {
				return 0, err
			}
			return 0, io.EOF
		}
		time.Sleep(t.poll)
	}
}

// closed adapts a signal channel into the done() predicate tailReader polls.
func closed(c <-chan struct{}) func() bool {
	return func() bool {
		select {
		case <-c:
			return true
		default:
			return false
		}
	}
}

// Package transfer runs rsync transfers (downloads and uploads) in the
// background, parsing rsync's progress output into a stream of events the UI can
// render and cancel.
package transfer

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/rjayasin/rtr/internal/config"
	"github.com/rjayasin/rtr/internal/util"
)

// Job describes one rsync transfer between this machine and the bookmark's host.
// For a download (the default) Sources are remote paths copied into LocalDest on
// this machine; for an upload (Upload set) Sources are local paths copied into
// RemoteDest on the host.
type Job struct {
	Bookmark   config.Bookmark
	Sources    []string // absolute source paths (remote for download, local for upload)
	LocalDest  string   // download destination directory (local)
	RemoteDest string   // upload destination directory (remote)
	Upload     bool
	Cfg        config.RsyncConfig
}

// sshTransport builds the `ssh ...` string passed to rsync's -e option, carrying
// the bookmark's port, identity, and jump host. Returns "" when no non-default
// options are needed (rsync then uses its built-in ssh, honoring ~/.ssh/config).
func sshTransport(b config.Bookmark) string {
	parts := []string{"ssh"}
	if p := b.EffectivePort(); p != 22 {
		parts = append(parts, "-p", fmt.Sprintf("%d", p))
	}
	if b.Identity != "" {
		parts = append(parts, "-i", util.ExpandHome(b.Identity))
	}
	if b.JumpHost != "" {
		parts = append(parts, "-J", b.JumpHost)
	}
	if len(parts) == 1 {
		return ""
	}
	return strings.Join(parts, " ")
}

// BuildArgs assembles the full rsync argument vector (excluding the binary name).
// Base flags come from config; rtr always appends --info=progress2 (machine
// readable overall progress) and --no-inc-recursive (so rsync sizes the whole
// transfer up front, making the percentage meaningful).
func BuildArgs(j Job) []string {
	flags := j.Cfg.Flags
	if len(flags) == 0 {
		flags = []string{"-a", "-z", "--partial", "--human-readable"}
	}
	args := make([]string, 0, len(flags)+len(j.Cfg.ExtraArgs)+len(j.Sources)+5)
	args = append(args, flags...)
	// --info=progress2 drives the progress bar; --no-inc-recursive sizes the
	// whole transfer up front so the percentage is meaningful; -s/--secluded-args
	// transmits remote paths to the remote rsync without a remote shell parsing
	// them, so spaces and special characters need no quoting. rtr execs rsync
	// directly (no local shell), so the source paths below are passed verbatim.
	args = append(args, "--info=progress2", "--no-inc-recursive", "-s")
	args = append(args, j.Cfg.ExtraArgs...)

	if t := sshTransport(j.Bookmark); t != "" {
		args = append(args, "-e", t)
	}
	if j.Upload {
		// Local sources copied up into the remote destination directory.
		args = append(args, j.Sources...)
		args = append(args, j.Bookmark.Target()+":"+j.RemoteDest)
		return args
	}
	for _, src := range j.Sources {
		args = append(args, j.Bookmark.Target()+":"+src)
	}
	args = append(args, j.LocalDest)
	return args
}

// Event is one update emitted while rsync runs. Exactly one field group is set:
// a Progress sample, a raw output Line, or the terminal Done (with optional Err).
type Event struct {
	Progress *Progress
	Line     string
	Done     bool
	Err      error
}

// PreviewCommand returns a human-readable approximation of the command rtr will
// run, for display in the UI before a transfer starts.
func (j Job) PreviewCommand() string {
	bin := j.Cfg.Binary
	if bin == "" {
		bin = "rsync"
	}
	return bin + " " + strings.Join(BuildArgs(j), " ")
}

// Start launches rsync and streams Events on the returned channel until the
// process exits, at which point it sends a final Event{Done:true} and closes the
// channel. Cancel the context to abort the transfer.
func Start(ctx context.Context, j Job) (<-chan Event, error) {
	bin := j.Cfg.Binary
	if bin == "" {
		bin = "rsync"
	}
	cmd := exec.CommandContext(ctx, bin, BuildArgs(j)...)
	// rsync forks worker processes and spawns ssh as the transport. Run it in its
	// own process group and, on cancel, kill the whole group — otherwise the
	// default cancel SIGKILLs only the main rsync process and its children keep
	// transferring in the background. WaitDelay bounds how long Wait blocks if a
	// child lingers holding the output pipe.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	// rsync writes progress to stdout and diagnostics to stderr; StdoutPipe sets
	// cmd.Stdout to the pipe's write end, so pointing Stderr at it merges both
	// streams into one reader and the UI log shows errors inline.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(stdout)
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
		scanErr := sc.Err()
		err := cmd.Wait()
		if err == nil {
			err = scanErr // surface a read error only if the process itself succeeded
		}
		ch <- Event{Done: true, Err: err}
	}()
	return ch, nil
}

package transfer

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/rjayasin/rtr/internal/config"
)

// Job describes one remote->local rsync pull: one or more remote source paths on
// the bookmark's host, copied into LocalDest on this machine.
type Job struct {
	Bookmark  config.Bookmark
	Sources   []string // absolute remote paths
	LocalDest string
	Cfg       config.RsyncConfig
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
		parts = append(parts, "-i", expandHome(b.Identity))
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
	args = append(args, "--info=progress2", "--no-inc-recursive")
	args = append(args, j.Cfg.ExtraArgs...)

	if t := sshTransport(j.Bookmark); t != "" {
		args = append(args, "-e", t)
	}
	for _, src := range j.Sources {
		args = append(args, j.Bookmark.Target()+":"+quoteRemote(src))
	}
	args = append(args, j.LocalDest)
	return args
}

// quoteRemote single-quotes a remote path so the remote shell that rsync invokes
// treats spaces and globbing characters literally.
func quoteRemote(p string) string {
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
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
		err := cmd.Wait()
		ch <- Event{Done: true, Err: err}
	}()
	return ch, nil
}

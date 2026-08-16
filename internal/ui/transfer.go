package ui

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rjayasin/rtr/internal/config"
	"github.com/rjayasin/rtr/internal/sshx"
	"github.com/rjayasin/rtr/internal/transfer"
	"github.com/rjayasin/rtr/internal/util"
)

// xfer is the live state of one background transfer, shown in the bottom panel.
// The rsync process is detached (it survives rtr quitting), so alongside the
// live handle it carries the identity persisted for re-attach on relaunch.
type xfer struct {
	id         int
	key        string          // persistent ID; stable across restarts, names the log file
	label      string          // file name, or "N items"
	dest       string          // local dir for a download, remote dir for an upload
	upload     bool            // direction: false = download (remote→local), true = upload
	bookmark   config.Bookmark // for persistence / auto-resume
	sources    []string        // source paths (remote for download, local for upload)
	pid        int             // detached rsync pid, once started (journaled for re-attach)
	logPath    string          // rsync output log, tailed for progress
	startedAt  time.Time
	finishedAt time.Time // set when the transfer ends; drives the completed-line stats
	pct        float64   // 0..100, interpolated between rsync's whole-percent samples
	rate       string
	eta        string
	bytes      int64  // total bytes transferred, from the latest progress sample
	last       string // last raw output line, used for error context
	done       bool
	cancelled  bool // user-cancelled (shown distinctly from a real error)
	respawned  bool // one-shot guard: a re-attached process that died was respawned
	err        error
	handle     *transfer.Handle

	// Sub-percent interpolation state (see applyProgress): the last whole
	// percent rsync reported, the byte count it was first seen at, and the
	// running estimate of how many bytes one percent of this transfer is.
	wholePct    float64
	pctBytes    int64
	bytesPerPct float64

	// partial-file cleanup, applied only when the user cancels: top-level
	// destination entries this job newly created, plus rsync temp-file globs.
	cleanupRemove []string
	cleanupGlobs  []string
}

// newXferKey mints a persistent transfer ID; it names the log file and links
// journal entries to processes across rtr restarts.
func newXferKey() string {
	return fmt.Sprintf("%d-%04x", time.Now().UnixNano(), rand.IntN(1<<16))
}

// kill stops the detached rsync process group, if one is known. It covers both
// a live handle and the pre-startedMsg window where only the pid (from a
// previous run's journal) is known.
func (x *xfer) kill() {
	if x.handle != nil {
		_ = x.handle.Kill()
		return
	}
	if x.pid > 0 {
		_ = (&transfer.Handle{PID: x.pid}).Kill()
	}
}

// job builds the rsync job for this transfer.
func (x *xfer) job(rc config.RsyncConfig) transfer.Job {
	if x.upload {
		return transfer.Job{Bookmark: x.bookmark, Sources: x.sources, RemoteDest: x.dest, Upload: true, Cfg: rc}
	}
	return transfer.Job{Bookmark: x.bookmark, Sources: x.sources, LocalDest: x.dest, Cfg: rc}
}

// activeTransfers counts transfers that are still running (not done/cancelled).
func (m model) activeTransfers() int {
	n := 0
	for _, x := range m.transfers {
		if !x.done && !x.cancelled {
			n++
		}
	}
	return n
}

// persistTransfers writes the still-running transfers to the resume file so
// rtr can re-attach to (or, if the process died, restart) them on the next
// launch, and clears it when none remain.
func (m model) persistTransfers() {
	var pend []config.PendingTransfer
	for _, x := range m.transfers {
		if x.done || x.cancelled {
			continue
		}
		pend = append(pend, config.PendingTransfer{
			ID:            x.key,
			Bookmark:      x.bookmark,
			Sources:       x.sources,
			Dest:          x.dest,
			Upload:        x.upload,
			PID:           x.pid,
			LogPath:       x.logPath,
			StartedAt:     x.startedAt,
			CleanupRemove: x.cleanupRemove,
			CleanupGlobs:  x.cleanupGlobs,
		})
	}
	_ = config.SavePendingTransfers(m.transfersPath, pend)
}

func (m model) findXfer(id int) *xfer {
	for _, x := range m.transfers {
		if x.id == id {
			return x
		}
	}
	return nil
}

// dropXfer removes the transfer with the given id from the panel, leaving the
// rest in place. Used by the cancelled-transfer linger timer.
func (m *model) dropXfer(id int) {
	kept := m.transfers[:0]
	for _, x := range m.transfers {
		if x.id != id {
			kept = append(kept, x)
		}
	}
	m.transfers = kept
}

// clearFinished drops completed or cancelled transfers, leaving running ones.
func (m *model) clearFinished() {
	kept := m.transfers[:0]
	for _, x := range m.transfers {
		if !x.done && !x.cancelled {
			kept = append(kept, x)
		}
	}
	m.transfers = kept
}

// handleEvent applies a transfer event to the matching background download and,
// unless it has finished, re-arms the wait command for that download.
func (m model) handleEvent(id int, ev transfer.Event) (tea.Model, tea.Cmd) {
	x := m.findXfer(id)
	if x == nil {
		return m, nil // transfer was cleared; drop the event
	}
	switch {
	case ev.Done:
		// A re-attached process (from a previous rtr run) that ends is not our
		// child, so its exit status is unknown. Unless the user cancelled it,
		// respawn once with --partial: a transfer that had already finished
		// verifies quickly and exits 0, an interrupted one resumes — and the
		// respawn is our child, so its exit status is real. respawned guards
		// against a loop.
		if errors.Is(ev.Err, transfer.ErrExitUnknown) && !x.cancelled && !x.respawned {
			x.respawned = true
			x.handle = nil
			x.pid = 0
			x.last = "resuming…"
			m.persistTransfers()
			return m, startCmd(x.id, x.job(m.cfg.Rsync), x.logPath)
		}
		x.done = true
		x.finishedAt = time.Now()
		x.err = ev.Err
		if ev.Err == nil {
			x.pct = 100
		}
		x.handle = nil
		if x.logPath != "" {
			os.Remove(x.logPath) // the log has no further use once the transfer ends
		}
		// The process has now exited, so it is safe to remove the partial files it
		// left behind. A download's partials are local and cleaned immediately; an
		// upload's are remote and cleaned in the background over SFTP.
		var cleanup tea.Cmd
		if x.cancelled {
			if x.upload {
				cleanup = m.remoteCleanupCmd(x)
			} else {
				cleanupPartial(x)
			}
		}
		m.persistTransfers() // drop the finished transfer from the resume file
		// If the pane showing the directory this transfer wrote to is open,
		// refresh it so the newly-arrived files appear: the local pane for a
		// download, the remote listing for an upload.
		if x.upload {
			if x.err == nil && m.session != nil && path.Clean(x.dest) == path.Clean(m.cwd) {
				return m, listCmd(m.session, m.cwd)
			}
			return m, cleanup
		}
		if m.localActive && filepath.Clean(x.dest) == filepath.Clean(m.localCwd) {
			m.reloadLocal()
		}
		return m, cleanup
	case ev.Progress != nil:
		x.applyProgress(*ev.Progress)
		return m, rearmCmd(id, x)
	default:
		if ev.Line != "" {
			x.last = ev.Line
		}
		return m, rearmCmd(id, x)
	}
}

// sizeScale converts the destination popover's measured source size into a
// bytes-per-percent seed for a transfer about to start. It is zero when the
// size is unknown (the walk is still running, or nothing was measured), in
// which case interpolation simply waits for rsync's own numbers.
func (m model) sizeScale() float64 {
	if m.sizeLoading || m.pendingSize <= 0 {
		return 0
	}
	return float64(m.pendingSize) / 100
}

// applyProgress folds one rsync sample into the transfer's live state.
//
// rsync's --info=progress2 reports whole percents only, so the bar would step in
// 1% jumps (and, on a bar narrower than 100 cells, in even coarser visual
// jumps). The byte counter printed alongside is much finer, so the displayed
// percentage is interpolated inside the current whole-percent bracket: bytes
// divided by percent is an estimate of the transfer's size per percent, and the
// bytes accumulated since this percent began say how far into it we are. The
// interpolation is clamped below the next whole percent, so it can only ever
// refine what rsync reported, never contradict it or run backwards within a
// bracket.
func (x *xfer) applyProgress(p transfer.Progress) {
	x.rate = p.Rate
	x.eta = p.ETA
	x.bytes = p.Bytes

	// A new bracket — or a restart, where a resumed transfer's byte count drops
	// back — re-anchors the interpolation and re-estimates the scale.
	if p.Percent != x.wholePct || p.Bytes < x.pctBytes {
		x.wholePct, x.pctBytes = p.Percent, p.Bytes
		if p.Percent > 0 && p.Bytes > 0 {
			x.bytesPerPct = float64(p.Bytes) / p.Percent
		}
	}
	x.pct = p.Percent
	if x.bytesPerPct > 0 && p.Bytes > x.pctBytes {
		x.pct += math.Min(float64(p.Bytes-x.pctBytes)/x.bytesPerPct, 0.99)
	}
}

// rearmCmd re-arms the event wait for a still-running transfer. A nil handle
// (a transfer between processes, or a synthetic test event) has no channel to
// wait on.
func rearmCmd(id int, x *xfer) tea.Cmd {
	if x.handle == nil {
		return nil
	}
	return waitEvCmd(id, x.handle.Events)
}

// computeCleanup builds the list of targets to delete if a transfer is
// cancelled — the top-level destination entries that did not already exist (so a
// pre-existing file is never destroyed) — plus rsync's temp-file glob for each
// source. base extracts a source's final element, join builds a destination
// path, and absent reports whether a target is confirmed not to exist yet; these
// differ between the local (download) and remote-over-SFTP (upload) cases.
func computeCleanup(dest string, sources []string, base func(string) string, join func(...string) string, absent func(string) bool) (remove, globs []string) {
	for _, s := range sources {
		b := base(s)
		target := join(dest, b)
		if absent(target) {
			remove = append(remove, target)
		}
		globs = append(globs, join(dest, "."+b+".??????"))
	}
	return remove, globs
}

// cleanupTargets computes what to delete if a download is cancelled. Sources are
// remote paths landing in the local dest, so existence is checked on the local
// filesystem.
func cleanupTargets(dest string, sources []string) (remove, globs []string) {
	return computeCleanup(dest, sources, path.Base, filepath.Join, func(p string) bool {
		_, err := os.Stat(p)
		return errors.Is(err, os.ErrNotExist)
	})
}

// cleanupPartial removes the partial files left by a cancelled transfer.
func cleanupPartial(x *xfer) {
	for _, p := range x.cleanupRemove {
		os.RemoveAll(p)
	}
	for _, g := range x.cleanupGlobs {
		matches, _ := filepath.Glob(g)
		for _, mt := range matches {
			os.Remove(mt)
		}
	}
}

// remoteCleanupTargets is cleanupTargets for an upload: it computes the remote
// entries to delete if the upload is cancelled — the top-level destination
// entries that did not already exist (so a pre-existing remote file is never
// destroyed), plus rsync's temp-file glob for each source. Existence is checked
// up front, while the session is known-good; a target whose Stat fails for any
// reason other than "not found" is left alone, never queued for deletion.
func remoteCleanupTargets(s *sshx.Session, dest string, sources []string) (remove, globs []string) {
	return computeCleanup(dest, sources, filepath.Base, path.Join, func(p string) bool {
		_, err := s.Stat(p)
		return errors.Is(err, os.ErrNotExist)
	})
}

// remoteCleanupCmd removes, in the background over SFTP, the partial files a
// cancelled upload left on the remote. It runs only when the current session is
// still connected to the very same host the upload used, so it can never touch
// the wrong machine; if the session is gone or now points elsewhere, the
// partials are left in place rather than risk deleting something unrelated.
func (m model) remoteCleanupCmd(x *xfer) tea.Cmd {
	s := m.session
	if s == nil || s.Bookmark != x.bookmark {
		return nil
	}
	remove := append([]string(nil), x.cleanupRemove...)
	globs := append([]string(nil), x.cleanupGlobs...)
	if len(remove) == 0 && len(globs) == 0 {
		return nil
	}
	return func() tea.Msg {
		for _, g := range globs {
			if matches, err := s.Glob(g); err == nil {
				for _, mt := range matches {
					s.RemoveAll(mt)
				}
			}
		}
		for _, p := range remove {
			s.RemoveAll(p)
		}
		return nil
	}
}

// xferNameWidth is the width of the file-name column in the transfers panel. It
// grows with the window (up to the longest label) so more of the name shows when
// there is room, while always leaving space for the marker, bar, and stats.
func (m model) xferNameWidth() int {
	// reserved: marker(2) + spaces(2) + progress bar + a rate/ETA stats budget.
	avail := m.width - m.barWidth - 26
	longest := 0
	for _, x := range m.transfers {
		if w := ansi.StringWidth(x.label); w > longest {
			longest = w
		}
	}
	w := longest
	if w > avail {
		w = avail
	}
	if w < 12 {
		w = 12
	}
	return w
}

// transfersHeight is the number of terminal rows the bottom panel occupies.
func (m model) transfersHeight() int {
	if len(m.transfers) == 0 {
		return 0
	}
	return len(m.transfers) + 1 // header + one row per transfer
}

// transfersView renders the stacked progress panel pinned to the bottom.
func (m model) transfersView() string {
	if len(m.transfers) == 0 {
		return ""
	}
	active := 0
	for _, x := range m.transfers {
		if !x.done && !x.cancelled {
			active++
		}
	}
	header := m.sectionLabel(focusTransfers, "transfers") + dimStyle.Render(fmt.Sprintf(" (%d active)", active))
	rows := []string{header}
	nw := m.xferNameWidth()
	for i, x := range m.transfers {
		marker := "  "
		if m.focus == focusTransfers && i == m.xferCursor {
			marker = cursorStyle.Render("➤ ")
		}
		// Direction arrow distinguishes uploads (↑) from downloads (↓).
		dir := dimStyle.Render("↓ ")
		if x.upload {
			dir = dimStyle.Render("↑ ")
		}
		name := padRight(truncate(x.label, nw), nw)
		var right string
		switch {
		case x.cancelled:
			right = errStyle.Render("✗") + " " + dimStyle.Render("cancelled")
		case x.done && x.err != nil:
			detail := x.err.Error()
			if x.last != "" {
				detail = x.last
			}
			right = errStyle.Render("✗") + " " + dimStyle.Render(truncate(detail, 44))
		case x.done:
			right = okStyle.Render("✓") + " " + dimStyle.Render("→ "+x.dest+x.doneStats())
		default:
			// The bar renders its own percentage; only append rate/ETA.
			var parts []string
			if x.rate != "" {
				parts = append(parts, x.rate)
			}
			if x.eta != "" {
				parts = append(parts, "ETA "+x.eta)
			}
			right = renderBar(m.barWidth, x.pct)
			if len(parts) > 0 {
				right += " " + dimStyle.Render(strings.Join(parts, " "))
			}
		}
		rows = append(rows, marker+dir+name+" "+right)
	}
	return strings.Join(rows, "\n")
}

// quitConfirmBox renders the "downloads in progress — quit anyway?" prompt.
func (m model) quitConfirmBox() string {
	inner := strings.Join([]string{
		errStyle.Render("Transfers in progress"),
		fmt.Sprintf("%d still running — they'll keep running in the", m.activeTransfers()),
		"background and reappear next launch.",
		"",
		"Quit?  " + helpStyle.Render("y / n"),
	}, "\n")
	return boxStyle.Width(clamp(m.width-8, 30, 56)).Render(inner)
}

// disconnectConfirmBox renders the "disconnect from host?" prompt shown when
// esc is pressed in the browser, with selectable Yes/No buttons.
func (m model) disconnectConfirmBox() string {
	host := "this host"
	if m.session != nil {
		host = m.session.Bookmark.Label()
	}
	buttons := choiceButton("Yes", m.disconnectChoice == 0) +
		"     " + choiceButton("No", m.disconnectChoice == 1)
	inner := strings.Join([]string{
		okStyle.Render("Disconnect"),
		"Disconnect from " + host + "?",
		"",
		buttons,
	}, "\n")
	return boxStyle.Width(clamp(m.width-8, 30, 56)).Align(lipgloss.Center).Render(inner)
}

// choiceButton renders a Yes/No button for the disconnect prompt. The
// accelerator (first letter) is always bold + underlined to hint the y/n
// shortcut; the selected button is highlighted and bracketed. Each character
// run is styled independently so the highlight survives the bold accelerator.
func choiceButton(text string, selected bool) string {
	fg := colDim
	if selected {
		fg = colAccent
	}
	base := lipgloss.NewStyle().Foreground(fg)
	accel := base.Bold(true).Underline(true)

	r := []rune(text)
	label := accel.Render(string(r[0]))
	if len(r) > 1 {
		label += base.Render(string(r[1:]))
	}
	if selected {
		bracket := base.Bold(true)
		return bracket.Render("[ ") + label + bracket.Render(" ]")
	}
	return "  " + label + "  "
}

// destPopover renders the local-destination prompt as a bordered box that is
// overlaid on top of the file list.
func (m model) destPopover() string {
	// Size is shown inline with the title: "Download N items • <size>" (or
	// "Upload …" for an upload), with a spinner standing in for the size while the
	// background walk is running.
	verb := "Download"
	if m.destUpload {
		verb = "Upload"
	}
	title := okStyle.Render(verb+" "+countLabel(len(m.pendingSources))) + dimStyle.Render(" • ")
	if m.sizeLoading {
		title += m.spinner.View() + dimStyle.Render(" calculating…")
	} else {
		title += dimStyle.Render(util.HumanBytes(m.pendingSize))
	}

	// List every selected file by name (not its full path), one per line, so the
	// user sees exactly what will be transferred.
	names := make([]string, len(m.pendingSources))
	for i, s := range m.pendingSources {
		names[i] = path.Base(s)
	}

	second := dimStyle.Render("Save to:")
	if m.destUpload {
		second = dimStyle.Render("Upload to:")
	}
	if m.err != nil {
		second = errStyle.Render(m.err.Error())
	}
	input := m.destInput.View()
	help := helpStyle.Render("enter start • esc cancel")

	// The box widens to fit the longest line (file names included) and is capped
	// to the terminal width, leaving room for the border; names are truncated
	// only when that cap is reached. boxStyle's width covers padding, so the text
	// area is two columns narrower than the width we set.
	textW := 0
	for _, l := range append([]string{title, second, input, help}, names...) {
		if w := ansi.StringWidth(l); w > textW {
			textW = w
		}
	}
	contentW := clamp(textW, 26, max(m.width-4, 26))
	for i, n := range names {
		names[i] = dimStyle.Render(truncate(n, contentW))
	}

	rows := append([]string{title}, names...)
	rows = append(rows, "", second, input, "", help)
	return boxStyle.Width(contentW + 2).Render(strings.Join(rows, "\n"))
}

// overlayCenter composites the box over the middle rows of base, centered
// horizontally. The base content to the left and right of the box is preserved
// (so file names beside the popover stay visible).
func overlayCenter(base []string, box string, width int) []string {
	boxLines := strings.Split(box, "\n")
	boxW := lipgloss.Width(box)
	left := (width - boxW) / 2
	if left < 0 {
		left = 0
	}
	start := 0
	if len(boxLines) < len(base) {
		start = (len(base) - len(boxLines)) / 2
	}
	out := make([]string, len(base))
	copy(out, base)
	for i, bl := range boxLines {
		row := start + i
		if row < 0 || row >= len(out) {
			continue
		}
		out[row] = overlayLine(out[row], bl, left, boxW)
	}
	return out
}

const ansiReset = "\x1b[0m"

// overlayLine places fg (a popover row of display width fgW) onto bg starting at
// column x, keeping bg's content to the left and right of that span. Slicing is
// display-column aware so styled cells aren't split mid-escape.
func overlayLine(bg, fg string, x, fgW int) string {
	leftPart := ansi.Truncate(bg, x, "")
	if w := ansi.StringWidth(leftPart); w < x {
		leftPart += strings.Repeat(" ", x-w) // pad short rows out to the box
	}
	rightPart := ansi.TruncateLeft(bg, x+fgW, "")
	return leftPart + ansiReset + fg + ansiReset + rightPart
}

// doneStats summarizes a completed transfer for its panel row: total size,
// elapsed wall time, and average rate, e.g. " • 4.2M in 1m5s • 66.0K/s avg".
// Empty when nothing was measured (no progress sample was ever seen).
func (x *xfer) doneStats() string {
	if x.bytes <= 0 || x.startedAt.IsZero() || x.finishedAt.Before(x.startedAt) {
		return ""
	}
	elapsed := x.finishedAt.Sub(x.startedAt)
	stats := fmt.Sprintf(" • %s in %s", util.HumanBytes(x.bytes), humanDuration(elapsed))
	if secs := elapsed.Seconds(); secs > 0 {
		stats += fmt.Sprintf(" • %s/s avg", util.HumanBytes(int64(float64(x.bytes)/secs)))
	}
	return stats
}

// humanDuration renders an elapsed time compactly at whole-second granularity.
func humanDuration(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	return d.Round(time.Second).String()
}

// ── small helpers ───────────────────────────────────────────────────

func countLabel(n int) string {
	if n == 1 {
		return "1 item"
	}
	return fmt.Sprintf("%d items", n)
}

func truncate(s string, w int) string {
	r := []rune(s)
	if w <= 1 || len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

func padRight(s string, w int) string {
	if n := w - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rjayasin/rtr/internal/config"
	"github.com/rjayasin/rtr/internal/sshx"
	"github.com/rjayasin/rtr/internal/transfer"
	"github.com/rjayasin/rtr/internal/util"
)

// xfer is the live state of one background transfer, shown in the bottom panel.
type xfer struct {
	id        int
	label     string          // file name, or "N items"
	dest      string          // local dir for a download, remote dir for an upload
	upload    bool            // direction: false = download (remote→local), true = upload
	bookmark  config.Bookmark // for persistence / auto-resume
	sources   []string        // source paths (remote for download, local for upload)
	pct       float64
	rate      string
	eta       string
	bytes     string
	last      string // last raw output line, used for error context
	done      bool
	cancelled bool // user-cancelled (shown distinctly from a real error)
	err       error
	ch        <-chan transfer.Event
	cancel    context.CancelFunc

	// partial-file cleanup, applied only when the user cancels: top-level
	// destination entries this job newly created, plus rsync temp-file globs.
	cleanupRemove []string
	cleanupGlobs  []string
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

// persistTransfers writes the still-running transfers to the resume file so they
// can be restarted on the next launch (and clears it when none remain).
func (m model) persistTransfers() {
	var pend []config.PendingTransfer
	for _, x := range m.transfers {
		if x.done || x.cancelled {
			continue
		}
		pend = append(pend, config.PendingTransfer{Bookmark: x.bookmark, Sources: x.sources, Dest: x.dest, Upload: x.upload})
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

// cancelTransfers stops every running download (used when leaving the browser).
func (m *model) cancelTransfers() {
	for _, x := range m.transfers {
		if x.cancel != nil {
			x.cancel()
			x.cancel = nil
		}
	}
	m.transfers = nil
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
		x.done = true
		x.err = ev.Err
		if ev.Err == nil {
			x.pct = 100
		}
		if x.cancel != nil {
			x.cancel()
			x.cancel = nil
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
		x.pct = ev.Progress.Percent
		x.rate = ev.Progress.Rate
		x.eta = ev.Progress.ETA
		x.bytes = ev.Progress.BytesRaw
		return m, waitEvCmd(id, x.ch)
	default:
		if ev.Line != "" {
			x.last = ev.Line
		}
		return m, waitEvCmd(id, x.ch)
	}
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
	avail := m.width - m.progress.Width - 26
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
			right = okStyle.Render("✓") + " " + dimStyle.Render("→ "+x.dest)
		default:
			// The bar renders its own percentage; only append rate/ETA.
			var parts []string
			if x.rate != "" {
				parts = append(parts, x.rate)
			}
			if x.eta != "" {
				parts = append(parts, "ETA "+x.eta)
			}
			right = m.progress.ViewAs(x.pct / 100)
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
		fmt.Sprintf("%d still running — they will resume next launch.", m.activeTransfers()),
		"",
		"Quit anyway?  " + helpStyle.Render("y / n"),
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

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
	"github.com/rjayasin/rtr/internal/transfer"
)

// xfer is the live state of one background download, shown in the bottom panel.
type xfer struct {
	id        int
	label     string // file name, or "N items"
	dest      string
	bookmark  config.Bookmark // for persistence / auto-resume
	sources   []string        // remote source paths
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
	return transfer.Job{Bookmark: x.bookmark, Sources: x.sources, LocalDest: x.dest, Cfg: rc}
}

// activeTransfers counts downloads that are still running (not done/cancelled).
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
		pend = append(pend, config.PendingTransfer{Bookmark: x.bookmark, Sources: x.sources, Dest: x.dest})
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
		if x.cancelled {
			// The process has now exited, so it is safe to remove the partial
			// files it left behind.
			cleanupPartial(x)
		}
		m.persistTransfers() // drop the finished transfer from the resume file
		return m, nil
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

// cleanupTargets computes what to delete if a download is cancelled: the
// top-level destination entries that did not already exist (so a pre-existing
// local file is never destroyed), plus rsync's temp-file glob for each source.
func cleanupTargets(dest string, sources []string) (remove, globs []string) {
	for _, s := range sources {
		base := path.Base(s)
		target := filepath.Join(dest, base)
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			remove = append(remove, target)
		}
		globs = append(globs, filepath.Join(dest, "."+base+".??????"))
	}
	return remove, globs
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
	header := dimStyle.Render(fmt.Sprintf("transfers (%d active)", active))
	if m.focus == focusTransfers {
		header = cursorStyle.Render("transfers ") + dimStyle.Render(fmt.Sprintf("(%d active)", active))
	}
	rows := []string{header}
	nw := m.xferNameWidth()
	for i, x := range m.transfers {
		marker := "  "
		if m.focus == focusTransfers && i == m.xferCursor {
			marker = cursorStyle.Render("▸ ")
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
		rows = append(rows, marker+name+" "+right)
	}
	return strings.Join(rows, "\n")
}

// quitConfirmBox renders the "downloads in progress — quit anyway?" prompt.
func (m model) quitConfirmBox() string {
	inner := strings.Join([]string{
		errStyle.Render("Downloads in progress"),
		fmt.Sprintf("%d still running — they will resume next launch.", m.activeTransfers()),
		"",
		"Quit anyway?  " + helpStyle.Render("y / n"),
	}, "\n")
	return boxStyle.Width(clamp(m.width-8, 30, 56)).Render(inner)
}

// destPopover renders the local-destination prompt as a bordered box that is
// overlaid on top of the file list.
func (m model) destPopover() string {
	title := okStyle.Render("Download " + countLabel(len(m.pendingSources)))
	what := dimStyle.Render(fmt.Sprintf("%d items", len(m.pendingSources)))
	if len(m.pendingSources) == 1 {
		what = dimStyle.Render(m.pendingSources[0])
	}
	second := dimStyle.Render("Save to:")
	if m.err != nil {
		second = errStyle.Render(m.err.Error())
	}
	inner := strings.Join([]string{
		title,
		truncate(what, 56),
		"",
		second,
		m.destInput.View(),
		"",
		helpStyle.Render("enter start • esc cancel"),
	}, "\n")
	return boxStyle.Width(clamp(m.width-10, 30, 64)).Render(inner)
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

func expandHomeUI(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
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

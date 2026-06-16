package ui

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rjayasin/rtr/internal/transfer"
)

// xfer is the live state of one background download, shown in the bottom panel.
type xfer struct {
	id     int
	label  string // file name, or "N items"
	dest   string
	pct    float64
	rate   string
	eta    string
	bytes  string
	last   string // last raw output line, used for error context
	done   bool
	err    error
	ch     <-chan transfer.Event
	cancel context.CancelFunc
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

// clearFinished drops completed transfers from the panel, leaving running ones.
func (m *model) clearFinished() {
	kept := m.transfers[:0]
	for _, x := range m.transfers {
		if !x.done {
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

const xferNameWidth = 22

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
		if !x.done {
			active++
		}
	}
	rows := []string{dimStyle.Render(fmt.Sprintf("transfers (%d active)", active))}
	for _, x := range m.transfers {
		name := padRight(truncate(x.label, xferNameWidth), xferNameWidth)
		var right string
		switch {
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
		rows = append(rows, name+" "+right)
	}
	return strings.Join(rows, "\n")
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

package ui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rjayasin/rtr/internal/transfer"
)

const maxLogLines = 8

// ── Destination prompt ──────────────────────────────────────────────

func (m model) updateDest(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			m.destInput.Blur()
			m.screen = screenBrowser
			return m, nil
		case "enter":
			dest := expandHomeUI(strings.TrimSpace(m.destInput.Value()))
			if dest == "" {
				m.err = fmt.Errorf("destination is required")
				return m, nil
			}
			if err := os.MkdirAll(dest, 0o755); err != nil {
				m.err = fmt.Errorf("create dest: %w", err)
				return m, nil
			}
			job := transfer.Job{
				Bookmark:  m.session.Bookmark,
				Sources:   m.pendingSources,
				LocalDest: dest,
				Cfg:       m.cfg.Rsync,
			}
			m.destInput.Blur()
			m.err = nil
			m.job = job
			m.screen = screenTransfer
			return m, startCmd(job)
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.destInput, cmd = m.destInput.Update(msg)
	return m, cmd
}

func (m model) viewDest() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("rtr — download") + "\n\n")
	b.WriteString(fmt.Sprintf("Downloading %s:\n", countLabel(len(m.pendingSources))))
	for _, s := range m.pendingSources {
		b.WriteString(dimStyle.Render("  • "+s) + "\n")
	}
	b.WriteString("\nLocal destination directory:\n")
	b.WriteString(m.destInput.View() + "\n\n")
	if m.err != nil {
		b.WriteString(errStyle.Render("error: ") + m.err.Error() + "\n\n")
	}
	b.WriteString(helpStyle.Render(helpDest))
	return b.String()
}

// ── Transfer / progress ─────────────────────────────────────────────

func (m model) handleEvent(ev transfer.Event) (tea.Model, tea.Cmd) {
	switch {
	case ev.Progress != nil:
		m.tpct = ev.Progress.Percent
		m.trate = ev.Progress.Rate
		m.teta = ev.Progress.ETA
		m.tbytes = ev.Progress.BytesRaw
		return m, waitEvCmd(m.evCh)
	case ev.Done:
		m.tdone = true
		m.terr = ev.Err
		if ev.Err == nil {
			m.tpct = 100
			m.selected = map[string]bool{}
		}
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		return m, nil
	default: // raw output line
		if ev.Line != "" {
			m.tlog = append(m.tlog, ev.Line)
			if len(m.tlog) > maxLogLines {
				m.tlog = m.tlog[len(m.tlog)-maxLogLines:]
			}
		}
		return m, waitEvCmd(m.evCh)
	}
}

func (m model) updateTransfer(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "q":
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	case "c":
		if !m.tdone && m.cancel != nil {
			m.cancel()
			m.cancel = nil
			m.status = "cancelling…"
		}
	case "enter", "esc":
		if m.tdone {
			m.screen = screenBrowser
			return m, listCmd(m.session, m.cwd)
		}
	}
	return m, nil
}

func (m model) viewTransfer() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("rtr — transfer") + "\n\n")

	b.WriteString(fmt.Sprintf("%s → %s\n", countLabel(len(m.job.Sources)), m.job.LocalDest))
	b.WriteString(dimStyle.Render(truncate(m.job.PreviewCommand(), maxWidth(m.width))) + "\n\n")

	b.WriteString(m.progress.ViewAs(m.tpct/100) + "\n")
	stats := fmt.Sprintf("%.0f%%", m.tpct)
	if m.tbytes != "" {
		stats += "  " + m.tbytes
	}
	if m.trate != "" {
		stats += "  " + m.trate
	}
	if m.teta != "" && !m.tdone {
		stats += "  ETA " + m.teta
	}
	b.WriteString(stats + "\n\n")

	if len(m.tlog) > 0 {
		b.WriteString(dimStyle.Render(strings.Join(m.tlog, "\n")) + "\n\n")
	}

	switch {
	case m.tdone && m.terr != nil:
		b.WriteString(errStyle.Render("✗ failed: ") + m.terr.Error() + "\n")
	case m.tdone:
		b.WriteString(okStyle.Render("✓ complete") + "\n")
	default:
		b.WriteString(m.spinner.View() + " transferring…\n")
	}
	b.WriteString("\n" + helpStyle.Render(helpTransfer))
	return b.String()
}

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
	if w <= 1 || len(s) <= w {
		return s
	}
	return s[:w-1] + "…"
}

func maxWidth(w int) int {
	if w <= 0 {
		return 80
	}
	return w - 2
}

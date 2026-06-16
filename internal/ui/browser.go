package ui

import (
	"fmt"
	"path"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) updateBrowser(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "q":
		m.closeSession()
		return m, tea.Quit
	case "esc":
		m.closeSession()
		m.screen = screenBookmarks
		return m, nil
	case "up", "k":
		if m.brCursor > 0 {
			m.brCursor--
		}
	case "down", "j":
		if m.brCursor < len(m.entries)-1 {
			m.brCursor++
		}
	case "right", "l", "enter":
		if e, ok := m.current(); ok && e.IsDir {
			return m, listCmd(m.session, e.Path)
		}
		// On a file, enter toggles selection for convenience.
		if e, ok := m.current(); ok {
			m.toggle(e.Path)
		}
	case "left", "h", "backspace":
		parent := path.Dir(m.cwd)
		if parent != m.cwd {
			return m, listCmd(m.session, parent)
		}
	case " ":
		if e, ok := m.current(); ok {
			m.toggle(e.Path)
		}
	case "a":
		for _, e := range m.entries {
			m.selected[e.Path] = true
		}
	case "c":
		m.selected = map[string]bool{}
	case "r":
		return m, listCmd(m.session, m.cwd)
	case "d":
		sources := m.selectionOrCurrent()
		if len(sources) == 0 {
			return m, nil
		}
		m.pendingSources = sources
		m.destInput.SetValue(m.cfg.DefaultLocalDir)
		m.destInput.Focus()
		m.destInput.CursorEnd()
		m.err = nil
		m.screen = screenDest
		return m, nil
	}
	m.clampScroll()
	return m, nil
}

func (m *model) toggle(p string) {
	if m.selected[p] {
		delete(m.selected, p)
	} else {
		m.selected[p] = true
	}
}

func (m model) current() (e entryRef, ok bool) {
	if m.brCursor < 0 || m.brCursor >= len(m.entries) {
		return entryRef{}, false
	}
	en := m.entries[m.brCursor]
	return entryRef{Name: en.Name, Path: en.Path, IsDir: en.IsDir, Size: en.Size}, true
}

// entryRef is a lightweight view of an sshx.Entry used by browser helpers.
type entryRef struct {
	Name  string
	Path  string
	IsDir bool
	Size  int64
}

// selectionOrCurrent returns checked paths, or the cursor's path if none checked.
func (m model) selectionOrCurrent() []string {
	var out []string
	for _, e := range m.entries { // preserve listing order
		if m.selected[e.Path] {
			out = append(out, e.Path)
		}
	}
	if len(out) == 0 {
		if e, ok := m.current(); ok {
			out = append(out, e.Path)
		}
	}
	return out
}

func (m *model) closeSession() {
	if m.session != nil {
		m.session.Close()
		m.session = nil
	}
	m.selected = map[string]bool{}
}

// clampScroll keeps the cursor within the visible window.
func (m *model) clampScroll() {
	rows := m.visibleRows()
	if m.brCursor < m.brOffset {
		m.brOffset = m.brCursor
	}
	if m.brCursor >= m.brOffset+rows {
		m.brOffset = m.brCursor - rows + 1
	}
	if m.brOffset < 0 {
		m.brOffset = 0
	}
}

func (m model) visibleRows() int {
	// title(2) + breadcrumb(2) + footer(3) chrome.
	r := m.height - 7
	if r < 3 {
		r = 3
	}
	return r
}

func (m model) viewBrowser() string {
	var b strings.Builder
	label := ""
	if m.session != nil {
		label = m.session.Bookmark.Label()
	}
	b.WriteString(titleStyle.Render("rtr — "+label) + "\n")
	b.WriteString(pathStyle.Render(m.cwd) + "\n\n")

	rows := m.visibleRows()
	end := m.brOffset + rows
	if end > len(m.entries) {
		end = len(m.entries)
	}
	if len(m.entries) == 0 {
		b.WriteString(dimStyle.Render("(empty directory)") + "\n")
	}
	for i := m.brOffset; i < end; i++ {
		e := m.entries[i]
		check := "[ ]"
		if m.selected[e.Path] {
			check = selectedStyle.Render("[x]")
		}
		name := e.Name
		size := "       "
		if e.IsDir {
			name = dirStyle.Render(name + "/")
		} else {
			size = dimStyle.Render(fmt.Sprintf("%8s", humanSize(e.Size)))
		}
		cursor := "  "
		if i == m.brCursor {
			cursor = cursorStyle.Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("%s%s %s  %s\n", cursor, check, size, name))
	}

	b.WriteString("\n")
	n := len(m.selected)
	status := fmt.Sprintf("%d selected", n)
	if m.err != nil {
		status = errStyle.Render("error: ") + m.err.Error()
	}
	b.WriteString(dimStyle.Render(status) + "\n")
	b.WriteString(helpStyle.Render(helpBrowser))
	return b.String()
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}

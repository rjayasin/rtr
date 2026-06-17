package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// localEntry is one item in the local file pane's listing.
type localEntry struct {
	name  string
	isDir bool
	size  int64
}

// toggleLocal opens or closes the local file pane. Opening loads the launch
// directory (once) and focuses the pane; closing returns focus to the remote list.
func (m model) toggleLocal() model {
	if m.localActive {
		m.localActive = false
		if m.focus == focusLocal {
			m.focus = focusFiles
		}
		return m
	}
	m.localActive = true
	if m.localCwd == "" {
		m.localCwd = m.startDir
	}
	m.loadLocal()
	m.focus = focusLocal
	return m
}

// updateLocalFocus handles keys while the local pane has focus.
func (m model) updateLocalFocus(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.localActive = false
		m.focus = focusFiles
		return m, nil
	case "up", "k":
		if m.localCursor > 0 {
			m.localCursor--
		}
	case "down", "j":
		if m.localCursor < len(m.localEntries)-1 {
			m.localCursor++
		}
	case "right":
		if e, ok := m.currentLocal(); ok && e.isDir {
			m.enterLocalDir(filepath.Join(m.localCwd, e.name))
		}
	case "left", "h", "backspace":
		if parent := filepath.Dir(m.localCwd); parent != m.localCwd {
			m.enterLocalDir(parent)
		}
	case "r":
		m.loadLocal()
	}
	m.clampLocalScroll()
	return m, nil
}

// defaultDest is the directory a new download is pre-filled with: the directory
// open in the local pane when it is showing, otherwise the launch directory.
func (m model) defaultDest() string {
	if m.localActive && m.localCwd != "" {
		return m.localCwd
	}
	return m.startDir
}

// loadLocal reads localCwd into localEntries, resetting the cursor.
func (m *model) loadLocal() {
	entries, err := readLocalDir(m.localCwd)
	m.localEntries = entries
	m.localErr = err
	m.localCursor, m.localOffset = 0, 0
}

func (m *model) enterLocalDir(dir string) {
	m.localCwd = dir
	m.loadLocal()
}

func (m model) currentLocal() (localEntry, bool) {
	if m.localCursor < 0 || m.localCursor >= len(m.localEntries) {
		return localEntry{}, false
	}
	return m.localEntries[m.localCursor], true
}

// clampLocalScroll keeps the local cursor within the visible window.
func (m *model) clampLocalScroll() {
	rows := m.visibleRows()
	if m.localCursor < m.localOffset {
		m.localOffset = m.localCursor
	}
	if m.localCursor >= m.localOffset+rows {
		m.localOffset = m.localCursor - rows + 1
	}
	if m.localOffset < 0 {
		m.localOffset = 0
	}
}

// readLocalDir lists a local directory, directories first then files, each
// sorted case-insensitively by name.
func readLocalDir(dir string) ([]localEntry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]localEntry, 0, len(des))
	for _, de := range des {
		var size int64
		if info, err := de.Info(); err == nil {
			size = info.Size()
		}
		out = append(out, localEntry{name: de.Name(), isDir: de.IsDir(), size: size})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].isDir != out[j].isDir {
			return out[i].isDir
		}
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	return out, nil
}

// localListLines renders exactly rows lines of the local listing (padded with
// blanks), mirroring the remote listLines layout minus the selection column.
func (m model) localListLines(rows int) []string {
	out := make([]string, 0, rows)
	switch {
	case m.localErr != nil:
		out = append(out, errStyle.Render("error: ")+m.localErr.Error())
	case len(m.localEntries) == 0:
		out = append(out, dimStyle.Render("(empty directory)"))
	}
	end := m.localOffset + rows
	if end > len(m.localEntries) {
		end = len(m.localEntries)
	}
	for i := m.localOffset; i < end; i++ {
		e := m.localEntries[i]
		name := e.name
		size := fmt.Sprintf("%8s", "")
		if e.isDir {
			name = dirStyle.Render(name + "/")
		} else {
			size = dimStyle.Render(fmt.Sprintf("%8s", humanSize(e.size)))
		}
		cursor := "  "
		if i == m.localCursor && m.focus == focusLocal {
			cursor = cursorStyle.Render("▸ ")
		}
		out = append(out, fmt.Sprintf("%s%s  %s", cursor, size, name))
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out[:rows]
}

// browserColumns renders the remote and local listings side by side, separated
// by a vertical divider. Each column carries its own breadcrumb header, so the
// combined block is the same height as the single-pane body (rows + 2).
func (m model) browserColumns() []string {
	rows := m.visibleRows()
	const sep = " │ "
	lw := (m.width - len(sep)) / 2
	if lw < 12 {
		lw = 12
	}
	rw := m.width - len(sep) - lw
	if rw < 12 {
		rw = 12
	}

	left := append([]string{dimStyle.Render("remote: " + m.cwd), ""}, m.listLines(rows)...)
	right := append([]string{dimStyle.Render("local: " + m.localCwd), ""}, m.localListLines(rows)...)

	out := make([]string, len(left))
	for i := range left {
		out[i] = fitLine(left[i], lw) + dimStyle.Render(sep) + fitLine(right[i], rw)
	}
	return out
}

// fitLine truncates s to width w (ANSI-aware, with an ellipsis) and pads it with
// spaces to exactly w columns so adjacent columns stay aligned.
func fitLine(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

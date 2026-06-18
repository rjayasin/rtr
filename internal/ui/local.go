package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// localEntry is one item in the local file pane's listing.
type localEntry struct {
	name    string
	isDir   bool
	size    int64
	modTime time.Time
}

// toggleLocal opens or closes the local file pane. Opening loads the launch
// directory (once) and focuses the pane; closing returns focus to the remote list.
func (m model) toggleLocal() model {
	if m.localActive {
		m.localActive = false
		m.compareMode = false
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
		// A live filter is cleared first; a second esc closes the pane.
		if m.localSearchInput.Value() != "" {
			m.clearLocalSearch()
			return m, nil
		}
		m.localActive = false
		m.focus = focusFiles
		return m, nil
	case "/":
		m.localSearchActive = true
		m.localSearchInput.Focus()
		m.localSearchInput.CursorEnd()
		return m, textinput.Blink
	case "up", "k":
		if m.localCursor > 0 {
			m.localCursor--
		}
	case "down", "j":
		if m.localCursor < len(m.displayedLocalEntries())-1 {
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
	case "t":
		// Sort by modification time; pressing t again flips newest/oldest.
		if m.localSort == sortTimeDesc {
			m.localSort = sortTimeAsc
		} else {
			m.localSort = sortTimeDesc
		}
		m.resortLocal()
	case "n":
		// Sort by name; pressing n again flips A→Z / Z→A.
		if m.localSort == sortNameAsc {
			m.localSort = sortNameDesc
		} else {
			m.localSort = sortNameAsc
		}
		m.resortLocal()
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

// loadLocal reads localCwd into localEntries, sorted by the active mode, and
// resets the cursor and any active filter (navigation/refresh clears the search,
// matching the remote pane).
func (m *model) loadLocal() {
	entries, err := readLocalDir(m.localCwd)
	m.localEntries = entries
	m.localErr = err
	sortLocalEntries(m.localEntries, m.localSort)
	m.clearLocalSearch()
	m.localCursor, m.localOffset = 0, 0
}

// updateLocalSearch drives the local pane's search field: typing filters live,
// enter accepts the filter and returns to the list, esc clears it.
func (m model) updateLocalSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		m.localSearchInput, cmd = m.localSearchInput.Update(msg)
		return m, cmd
	}
	switch key.String() {
	case "esc":
		m.clearLocalSearch()
		return m, nil
	case "enter":
		m.localSearchActive = false
		m.localSearchInput.Blur()
		m.localCursor, m.localOffset = 0, 0
		return m, nil
	}
	var cmd tea.Cmd
	m.localSearchInput, cmd = m.localSearchInput.Update(msg)
	m.localCursor, m.localOffset = 0, 0
	return m, cmd
}

// clearLocalSearch dismisses the local search field and removes the filter.
func (m *model) clearLocalSearch() {
	m.localSearchActive = false
	m.localSearchInput.Blur()
	m.localSearchInput.SetValue("")
	m.localCursor, m.localOffset = 0, 0
}

// filteredLocalEntries returns the local entries matching the current query
// (case-insensitive substring on the name), or all entries when no query is set.
func (m model) filteredLocalEntries() []localEntry {
	q := strings.ToLower(strings.TrimSpace(m.localSearchInput.Value()))
	if q == "" {
		return m.localEntries
	}
	out := make([]localEntry, 0, len(m.localEntries))
	for _, e := range m.localEntries {
		if strings.Contains(strings.ToLower(e.name), q) {
			out = append(out, e)
		}
	}
	return out
}

// resortLocal re-orders the current local listing and returns the cursor to top.
func (m *model) resortLocal() {
	sortLocalEntries(m.localEntries, m.localSort)
	m.localCursor, m.localOffset = 0, 0
}

// reloadLocal re-reads the current local directory in place, preserving the
// active filter and (clamped) cursor. Used to refresh after a download lands.
func (m *model) reloadLocal() {
	entries, err := readLocalDir(m.localCwd)
	m.localEntries = entries
	m.localErr = err
	sortLocalEntries(m.localEntries, m.localSort)
	if n := len(m.displayedLocalEntries()); m.localCursor >= n {
		m.localCursor = n - 1
	}
	if m.localCursor < 0 {
		m.localCursor = 0
	}
	m.clampLocalScroll()
}

func (m *model) enterLocalDir(dir string) {
	m.localCwd = dir
	m.loadLocal()
}

func (m model) currentLocal() (localEntry, bool) {
	es := m.displayedLocalEntries()
	if m.localCursor < 0 || m.localCursor >= len(es) {
		return localEntry{}, false
	}
	return es[m.localCursor], true
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

// readLocalDir lists a local directory (unsorted; the caller applies the chosen
// sort mode).
func readLocalDir(dir string) ([]localEntry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]localEntry, 0, len(des))
	for _, de := range des {
		var size int64
		var mod time.Time
		if info, err := de.Info(); err == nil {
			size = info.Size()
			mod = info.ModTime()
		}
		out = append(out, localEntry{name: de.Name(), isDir: de.IsDir(), size: size, modTime: mod})
	}
	return out, nil
}

// sortLocalEntries orders entries in place by the chosen key, with directories
// and files interspersed — matching the remote pane's sortEntries semantics.
func sortLocalEntries(entries []localEntry, mode sortMode) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		switch mode {
		case sortTimeDesc:
			if !a.modTime.Equal(b.modTime) {
				return a.modTime.After(b.modTime) // newest first
			}
			return strings.ToLower(a.name) < strings.ToLower(b.name)
		case sortTimeAsc:
			if !a.modTime.Equal(b.modTime) {
				return a.modTime.Before(b.modTime) // oldest first
			}
			return strings.ToLower(a.name) < strings.ToLower(b.name)
		case sortNameDesc:
			return strings.ToLower(a.name) > strings.ToLower(b.name)
		default: // sortNameAsc
			return strings.ToLower(a.name) < strings.ToLower(b.name)
		}
	})
}

// localListLines renders exactly rows lines of the local listing (padded with
// blanks), mirroring the remote listLines layout minus the selection column.
func (m model) localListLines(rows int) []string {
	entries := m.displayedLocalEntries()
	var common map[string]bool
	if m.comparing() {
		common = m.commonNames()
	}
	out := make([]string, 0, rows)
	switch {
	case m.localErr != nil:
		out = append(out, errStyle.Render("error: ")+m.localErr.Error())
	case len(entries) == 0 && m.localSearchInput.Value() != "":
		out = append(out, dimStyle.Render("(no matches)"))
	case len(entries) == 0:
		out = append(out, dimStyle.Render("(empty directory)"))
	}
	end := m.localOffset + rows
	if end > len(entries) {
		end = len(entries)
	}
	for i := m.localOffset; i < end; i++ {
		e := entries[i]
		cursor := "  "
		if i == m.localCursor && m.focus == focusLocal {
			cursor = cursorStyle.Render("▸ ")
		}
		out = append(out, fmt.Sprintf("%s%s  %s", cursor, sizeCell(e.isDir, common[e.name], e.size), nameCell(e.name, e.isDir, common[e.name])))
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

	remoteHead := dimStyle.Render("remote: "+m.cwd) + searchSuffix(m.searchActive, m.searchInput.Value())
	localHead := dimStyle.Render("local: "+m.localCwd) + searchSuffix(m.localSearchActive, m.localSearchInput.Value())
	left := append([]string{remoteHead, ""}, m.listLines(rows)...)
	right := append([]string{localHead, ""}, m.localListLines(rows)...)

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

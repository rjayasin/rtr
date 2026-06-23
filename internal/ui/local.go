package ui

import (
	"fmt"
	"os"
	"path/filepath"
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
	case ".":
		// Toggle dot-file visibility (shared with the remote pane); start from the
		// top since the visible set changes.
		m.showHidden = !m.showHidden
		m.localCursor, m.localOffset = 0, 0
	case "r":
		m.loadLocal()
	case "enter":
		// Mirror of the remote pane's download: open the destination popover for
		// the file/directory under the cursor, prefilled with the remote dir, and
		// queue a background upload on confirm. Requires an active connection.
		e, ok := m.currentLocal()
		if !ok || m.session == nil {
			return m, nil
		}
		src := filepath.Join(m.localCwd, e.name)
		m.pendingSources = []string{src}
		m.pendingSize = 0
		m.sizeReqID++
		m.sizeLoading = true
		sizeCalc := localSizeCmd(m.sizeReqID, m.pendingSources)
		m.destInput.SetValue(m.defaultUploadDest())
		m.destInput.Focus()
		m.destInput.CursorEnd()
		m.destActive = true
		m.destUpload = true
		m.err = nil
		return m, sizeCalc
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

// defaultUploadDest is the remote directory a new upload is pre-filled with: the
// directory currently open in the remote pane.
func (m model) defaultUploadDest() string {
	return m.cwd
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

// filteredLocalEntries returns the local entries to display: dot files are
// dropped unless showHidden is on, and the remainder is narrowed to the current
// query (case-insensitive substring on the name).
func (m model) filteredLocalEntries() []localEntry {
	return filterByName(m.localEntries, func(e localEntry) string { return e.name }, m.showHidden, m.localSearchInput.Value())
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

// sortLocalEntries orders a local listing in place by the chosen key, matching
// the remote pane's sortEntries semantics.
func sortLocalEntries(entries []localEntry, mode sortMode) {
	sortByMode(entries, mode,
		func(e localEntry) string { return e.name },
		func(e localEntry) time.Time { return e.modTime })
}

// localListLines renders exactly rows lines of the local listing (padded with
// blanks), mirroring the remote listLines layout minus the selection column.
func (m model) localListLines(rows int, common map[string]bool) []string {
	entries := m.displayedLocalEntriesWith(common)
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
		cursorRow := i == m.localCursor && m.focus == focusLocal
		marker := "  "
		if cursorRow {
			marker = cursorStyle.Render("➤ ")
		}
		out = append(out, fmt.Sprintf("%s%s  %s",
			marker,
			sizeCell(e.isDir, common[e.name], e.size, cursorRow),
			nameCell(e.name, e.isDir, common[e.name], cursorRow)))
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out[:rows]
}

// browserColumns renders the remote and local listings side by side, separated
// by a vertical divider. Each column carries its own breadcrumb header, so the
// combined block is the same height as the single-pane body (rows + 2).
func (m model) browserColumns(common map[string]bool) []string {
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

	remoteHead := m.sectionLabel(focusFiles, "remote") + dimStyle.Render(" "+m.cwd) + searchSuffix(m.searchActive, m.searchInput.Value())
	localHead := m.sectionLabel(focusLocal, "local") + dimStyle.Render(" "+m.localCwd) + searchSuffix(m.localSearchActive, m.localSearchInput.Value())
	left := append([]string{remoteHead, ""}, m.listLines(rows, common)...)
	right := append([]string{localHead, ""}, m.localListLines(rows, common)...)

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

// spread lays left and right on one line of the given width — left-justified and
// right-justified respectively (ANSI-aware), with at least one space between.
func spread(left, right string, width int) string {
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

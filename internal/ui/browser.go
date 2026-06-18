package ui

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/rjayasin/rtr/internal/sshx"
	"github.com/rjayasin/rtr/internal/transfer"
)

// sortMode is the remote listing order. `t` cycles the time modes and `n`
// cycles the name modes (each press flips direction). Directories and files
// are interspersed (sorted together by the key, not grouped by type).
type sortMode int

const (
	sortTimeDesc sortMode = iota // most-recently-modified first (default)
	sortTimeAsc                  // oldest-modified first
	sortNameAsc                  // A → Z
	sortNameDesc                 // Z → A
)

func (s sortMode) String() string {
	switch s {
	case sortTimeDesc:
		return "newest"
	case sortTimeAsc:
		return "oldest"
	case sortNameDesc:
		return "name ↓"
	default:
		return "name ↑"
	}
}

// sortEntries orders entries in place by the chosen key, with directories and
// files interspersed (name is the tie-break for the time modes).
func sortEntries(entries []sshx.Entry, mode sortMode) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		switch mode {
		case sortTimeDesc:
			if !a.ModTime.Equal(b.ModTime) {
				return a.ModTime.After(b.ModTime) // newest first
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		case sortTimeAsc:
			if !a.ModTime.Equal(b.ModTime) {
				return a.ModTime.Before(b.ModTime) // oldest first
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		case sortNameDesc:
			return strings.ToLower(a.Name) > strings.ToLower(b.Name)
		default: // sortNameAsc
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
	})
}

// focusArea selects which pane the arrow keys scroll.
type focusArea int

const (
	focusFiles     focusArea = iota // remote file list
	focusLocal                      // local file pane
	focusTransfers                  // background transfers panel
)

func (m model) updateBrowser(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.destActive {
		return m.updateDestPopover(msg)
	}
	if m.searchActive {
		return m.updateSearch(msg)
	}
	if m.localSearchActive {
		return m.updateLocalSearch(msg)
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		if m.focus == focusLocal {
			return m.updateLocalFocus(key)
		}
		return m.updateFileFocus(key)
	}
	return m, nil
}

// updateFileFocus handles keys while the file list has scroll focus. Quit and
// the transfers panel (tab toggle) are handled globally in handleGlobalKey.
func (m model) updateFileFocus(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		// A live filter is cleared first; a second esc prompts to disconnect.
		if m.searchInput.Value() != "" {
			m.clearSearch()
			return m, nil
		}
		// Confirm before leaving the browser. Downloads keep running either way
		// (they show on the bookmarks screen and resume on the next launch).
		m.confirmDisconnect = true
		m.disconnectChoice = 1 // default to No
		return m, nil
	case "/":
		// Open the search field, keeping any existing query so it can be edited.
		m.searchActive = true
		m.searchInput.Focus()
		m.searchInput.CursorEnd()
		return m, textinput.Blink
	case "up", "k":
		if m.brCursor > 0 {
			m.brCursor--
		}
	case "down", "j":
		if m.brCursor < len(m.filteredEntries())-1 {
			m.brCursor++
		}
	case "right":
		if e, ok := m.current(); ok && e.IsDir {
			return m, listCmd(m.session, e.Path)
		}
		// On a file, → toggles selection for convenience.
		if e, ok := m.current(); ok {
			m.toggle(e.Path)
		}
	case "left", "h", "backspace":
		parent := path.Dir(m.cwd)
		if parent != m.cwd {
			return m, listCmd(m.session, parent)
		}
	case "x", " ":
		if e, ok := m.current(); ok {
			m.toggle(e.Path)
		}
	case "a":
		for _, e := range m.filteredEntries() {
			m.selected[e.Path] = true
		}
	case "c":
		m.selected = map[string]bool{}
	case "t":
		// Sort by modification time; pressing t again flips newest/oldest.
		if m.sortMode == sortTimeDesc {
			m.sortMode = sortTimeAsc
		} else {
			m.sortMode = sortTimeDesc
		}
		m.resort()
	case "n":
		// Sort by name; pressing n again flips A→Z / Z→A.
		if m.sortMode == sortNameAsc {
			m.sortMode = sortNameDesc
		} else {
			m.sortMode = sortNameAsc
		}
		m.resort()
	case "r":
		return m, listCmd(m.session, m.cwd)
	case "enter":
		// Enter always opens the download prompt for the selection (or, if
		// nothing is checked, the entry under the cursor — file or directory).
		// Use → to navigate into a directory instead.
		sources := m.selectionOrCurrent()
		if len(sources) == 0 {
			return m, nil
		}
		m.pendingSources = sources
		m.pendingSize = 0
		m.sizeReqID++
		var sizeCalc tea.Cmd
		if m.session != nil {
			m.sizeLoading = true
			sizeCalc = sizeCmd(m.sizeReqID, m.session, sources)
		}
		m.destInput.SetValue(m.defaultDest())
		m.destInput.Focus()
		m.destInput.CursorEnd()
		m.destActive = true
		m.err = nil
		return m, sizeCalc
	}
	m.clampScroll()
	return m, nil
}

// updateTransferFocus handles keys while the transfers panel has scroll focus.
func (m model) updateTransferFocus(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.transfers) == 0 {
		m.focus = focusFiles
		return m, nil
	}
	switch key.String() {
	case "esc":
		m.focus = focusFiles
	case "up", "k":
		if m.xferCursor > 0 {
			m.xferCursor--
		}
	case "down", "j":
		if m.xferCursor < len(m.transfers)-1 {
			m.xferCursor++
		}
	case "c":
		// Cancel the highlighted transfer if it is still running, then arm a
		// timer to remove the cancelled row from the panel after a short linger.
		x := m.transfers[m.xferCursor]
		if !x.done && x.cancel != nil {
			x.cancel()
			x.cancel = nil
			x.cancelled = true
			m.persistTransfers()
			return m, dropXferCmd(x.id)
		}
	case "x":
		m.clearFinished()
		m.clampXferCursor()
		if len(m.transfers) == 0 {
			m.focus = focusFiles
		}
	}
	return m, nil
}

func (m *model) clampXferCursor() {
	if m.xferCursor >= len(m.transfers) {
		m.xferCursor = len(m.transfers) - 1
	}
	if m.xferCursor < 0 {
		m.xferCursor = 0
	}
}

// updateDestPopover drives the local-destination popover. Enter queues a
// background transfer and returns to browsing; esc dismisses the popover.
func (m model) updateDestPopover(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		m.destInput, cmd = m.destInput.Update(msg)
		return m, cmd
	}
	switch key.String() {
	case "esc":
		m.destActive = false
		m.destInput.Blur()
		m.err = nil
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
		id := m.nextXfer
		m.nextXfer++
		job := transfer.Job{
			Bookmark:  m.session.Bookmark,
			Sources:   m.pendingSources,
			LocalDest: dest,
			Cfg:       m.cfg.Rsync,
		}
		remove, globs := cleanupTargets(dest, m.pendingSources)
		m.transfers = append(m.transfers, &xfer{
			id:            id,
			label:         transferLabel(m.pendingSources),
			dest:          dest,
			bookmark:      m.session.Bookmark,
			sources:       m.pendingSources,
			cleanupRemove: remove,
			cleanupGlobs:  globs,
		})
		m.destActive = false
		m.destInput.Blur()
		m.selected = map[string]bool{} // ready for the next selection
		m.err = nil
		m.persistTransfers()
		return m, startCmd(id, job)
	}
	var cmd tea.Cmd
	m.destInput, cmd = m.destInput.Update(msg)
	return m, cmd
}

// updateSearch drives the browser search field. Typing filters the listing
// live; enter accepts the filter and returns to list navigation; esc cancels
// the search and clears the filter.
func (m model) updateSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		return m, cmd
	}
	switch key.String() {
	case "esc":
		m.clearSearch()
		return m, nil
	case "enter":
		// Keep the filter applied but hand keys back to the list.
		m.searchActive = false
		m.searchInput.Blur()
		m.brCursor, m.brOffset = 0, 0
		return m, nil
	}
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	// The match set may have changed; restart from the top of the results.
	m.brCursor, m.brOffset = 0, 0
	return m, cmd
}

// clearSearch dismisses the search field and removes any active filter.
func (m *model) clearSearch() {
	m.searchActive = false
	m.searchInput.Blur()
	m.searchInput.SetValue("")
	m.brCursor, m.brOffset = 0, 0
}

// filteredEntries returns the entries matching the current search query
// (case-insensitive substring on the name), or all entries when no query is set.
func (m model) filteredEntries() []sshx.Entry {
	q := strings.ToLower(strings.TrimSpace(m.searchInput.Value()))
	if q == "" {
		return m.entries
	}
	out := make([]sshx.Entry, 0, len(m.entries))
	for _, e := range m.entries {
		if strings.Contains(strings.ToLower(e.Name), q) {
			out = append(out, e)
		}
	}
	return out
}

// transferLabel names a queued download for the panel.
func transferLabel(sources []string) string {
	if len(sources) == 1 {
		return path.Base(sources[0])
	}
	return fmt.Sprintf("%d items", len(sources))
}

func (m *model) toggle(p string) {
	if m.selected[p] {
		delete(m.selected, p)
	} else {
		m.selected[p] = true
	}
}

// resort re-orders the current listing for the active sort mode and returns the
// cursor to the top.
func (m *model) resort() {
	sortEntries(m.entries, m.sortMode)
	m.brCursor, m.brOffset = 0, 0
}

func (m model) current() (e entryRef, ok bool) {
	es := m.filteredEntries()
	if m.brCursor < 0 || m.brCursor >= len(es) {
		return entryRef{}, false
	}
	en := es[m.brCursor]
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
	// chrome: title + breadcrumb + blank + status + footer = 5 lines. When there
	// are transfers, add the divider plus the panel.
	chrome := 5
	if len(m.transfers) > 0 {
		chrome += 1 + m.transfersHeight() // divider + panel
	}
	if m.searchActive || m.localSearchActive {
		chrome++ // the active search field sits above the footer
	}
	r := m.height - chrome
	if r < 3 {
		r = 3
	}
	return r
}

// listLines renders exactly rows lines of the directory listing (padded with
// blanks), so the status/panel/footer stay pinned to the bottom of the window.
func (m model) listLines(rows int) []string {
	entries := m.filteredEntries()
	out := make([]string, 0, rows)
	if len(entries) == 0 {
		if m.searchInput.Value() != "" {
			out = append(out, dimStyle.Render("(no matches)"))
		} else {
			out = append(out, dimStyle.Render("(empty directory)"))
		}
	}
	end := m.brOffset + rows
	if end > len(entries) {
		end = len(entries)
	}
	for i := m.brOffset; i < end; i++ {
		e := entries[i]
		check := "[ ]"
		if m.selected[e.Path] {
			check = selectedStyle.Render("[x]")
		}
		name := e.Name
		size := fmt.Sprintf("%8s", "") // align with the %8s file-size column
		if e.IsDir {
			name = dirStyle.Render(name + "/")
		} else {
			size = dimStyle.Render(fmt.Sprintf("%8s", humanSize(e.Size)))
		}
		cursor := "  "
		if i == m.brCursor && m.focus == focusFiles {
			cursor = cursorStyle.Render("▸ ")
		}
		out = append(out, fmt.Sprintf("%s%s %s  %s", cursor, check, size, name))
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out[:rows]
}

func (m model) viewBrowser() string {
	label := ""
	if m.session != nil {
		label = m.session.Bookmark.Label()
	}

	lines := []string{titleStyle.Render("rtr — " + label)}

	var body []string
	if m.localActive {
		body = m.browserColumns()
	} else {
		breadcrumb := pathStyle.Render(m.cwd) + searchSuffix(m.searchActive, m.searchInput.Value())
		body = append([]string{breadcrumb, ""}, m.listLines(m.visibleRows())...)
	}
	if m.destActive {
		body = overlayCenter(body, m.destPopover(), max(m.width, 1))
	}
	lines = append(lines, body...)

	status := ""
	if n := len(m.selected); n > 0 {
		status = fmt.Sprintf("%d selected", n)
	}
	if m.err != nil && !m.destActive {
		status = errStyle.Render("error: ") + m.err.Error()
	}
	lines = append(lines, dimStyle.Render(status))

	if panel := m.transfersView(); panel != "" {
		lines = append(lines, dividerLine(m.width)) // separate files from transfers
		lines = append(lines, strings.Split(panel, "\n")...)
	}
	// The active search field (remote or local — only one can be typing at a
	// time) sits just above the shortcut footer. Accepted-but-inactive filters
	// are shown as a suffix on each pane's breadcrumb instead.
	if m.searchActive {
		lines = append(lines, m.searchInput.View())
	} else if m.localSearchActive {
		lines = append(lines, m.localSearchInput.View())
	}
	browserHelp := fmt.Sprintf(
		"↑/↓ move • → open • ← up • x/space select • / search • l local • enter download • t/n sort:%s • a all • c clear • r refresh • esc back",
		m.sortMode)
	help := m.footer(browserHelp)
	if m.searchActive || m.localSearchActive {
		help = "type to filter • enter accept • esc clear"
	}
	lines = append(lines, helpStyle.Render(help))
	return strings.Join(lines, "\n")
}

// searchSuffix renders an accepted (non-active) filter as a breadcrumb suffix,
// e.g. "  /report". While the field is actively being typed, it shows above the
// footer instead, so nothing is appended here.
func searchSuffix(active bool, query string) string {
	if !active && query != "" {
		return dimStyle.Render("  /" + query)
	}
	return ""
}

func dividerLine(w int) string {
	if w < 1 {
		w = 1
	}
	return dimStyle.Render(strings.Repeat("─", w))
}

// footer renders the help line: the transfers-panel keys when that pane is
// focused, otherwise the given screen help plus a hint to reach the panel.
func (m model) footer(baseHelp string) string {
	switch m.focus {
	case focusTransfers:
		return "↑/↓ select • c cancel • x clear done • tab/esc files • q quit"
	case focusLocal:
		return fmt.Sprintf("↑/↓ move • → open • ← up • / search • t/n sort:%s • r refresh • l/esc close • tab remote • q quit", m.localSort)
	}
	switch {
	case m.localActive && len(m.transfers) > 0:
		return baseHelp + " • tab panes"
	case m.localActive:
		return baseHelp + " • tab local"
	case len(m.transfers) > 0:
		return baseHelp + " • tab transfers"
	}
	return baseHelp
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

package ui

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rjayasin/rtr/internal/sshx"
	"github.com/rjayasin/rtr/internal/transfer"
)

// sortMode is the remote listing order, toggled with `s`. Directories and files
// are interspersed (sorted together by the key, not grouped by type).
type sortMode int

const (
	sortName sortMode = iota // alphabetical
	sortTime                 // most-recently-modified first
)

func (s sortMode) String() string {
	if s == sortTime {
		return "time"
	}
	return "name"
}

// toggled returns the other sort mode.
func (s sortMode) toggled() sortMode {
	if s == sortName {
		return sortTime
	}
	return sortName
}

// sortEntries orders entries in place by the chosen key, with directories and
// files interspersed (name is the tie-break for time sorting).
func sortEntries(entries []sshx.Entry, mode sortMode) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if mode == sortTime && !a.ModTime.Equal(b.ModTime) {
			return a.ModTime.After(b.ModTime) // newest first
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

// focusArea selects which pane the arrow keys scroll.
type focusArea int

const (
	focusFiles focusArea = iota
	focusTransfers
)

func (m model) updateBrowser(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.destActive {
		return m.updateDestPopover(msg)
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		return m.updateFileFocus(key)
	}
	return m, nil
}

// updateFileFocus handles keys while the file list has scroll focus. Quit, the
// transfers panel, and the `t` toggle are handled globally in handleGlobalKey.
func (m model) updateFileFocus(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		// Leave the browser but keep downloads running (they show on the
		// bookmarks screen and resume on the next launch).
		m.closeSession()
		m.focus = focusFiles
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
	case "right", "l":
		if e, ok := m.current(); ok && e.IsDir {
			return m, listCmd(m.session, e.Path)
		}
		// On a file, →/l toggles selection for convenience.
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
		for _, e := range m.entries {
			m.selected[e.Path] = true
		}
	case "c":
		m.selected = map[string]bool{}
	case "s":
		m.sortMode = m.sortMode.toggled()
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
		m.destInput.SetValue(m.startDir)
		m.destInput.Focus()
		m.destInput.CursorEnd()
		m.destActive = true
		m.err = nil
		return m, nil
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
	// chrome: title + breadcrumb + blank + status + footer = 5 lines. When there
	// are transfers, add the divider plus the panel.
	chrome := 5
	if len(m.transfers) > 0 {
		chrome += 1 + m.transfersHeight() // divider + panel
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
	out := make([]string, 0, rows)
	if len(m.entries) == 0 {
		out = append(out, dimStyle.Render("(empty directory)"))
	}
	end := m.brOffset + rows
	if end > len(m.entries) {
		end = len(m.entries)
	}
	for i := m.brOffset; i < end; i++ {
		e := m.entries[i]
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

	lines := []string{
		titleStyle.Render("rtr — " + label),
		pathStyle.Render(m.cwd),
		"",
	}

	listLines := m.listLines(m.visibleRows())
	if m.destActive {
		listLines = overlayCenter(listLines, m.destPopover(), max(m.width, 1))
	}
	lines = append(lines, listLines...)

	status := fmt.Sprintf("%d selected", len(m.selected))
	if m.err != nil && !m.destActive {
		status = errStyle.Render("error: ") + m.err.Error()
	}
	lines = append(lines, dimStyle.Render(status))

	if panel := m.transfersView(); panel != "" {
		lines = append(lines, dividerLine(m.width)) // separate files from transfers
		lines = append(lines, strings.Split(panel, "\n")...)
	}
	browserHelp := fmt.Sprintf(
		"↑/↓ move • → open • ← up • x/space select • enter download • s sort:%s • a all • c clear • r refresh • esc back",
		m.sortMode)
	lines = append(lines, helpStyle.Render(m.footer(browserHelp)))
	return strings.Join(lines, "\n")
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
	if m.focus == focusTransfers {
		return "↑/↓ select • c cancel • x clear done • t/esc files • q quit"
	}
	if len(m.transfers) > 0 {
		return baseHelp + " • t transfers"
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

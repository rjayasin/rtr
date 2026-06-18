package ui

import (
	"fmt"

	"github.com/rjayasin/rtr/internal/sshx"
)

// commonNames returns the set of entry names present in BOTH the remote and the
// local listing of the currently-open directories. Comparison mode uses it to
// dim and sink files that exist in both places. Matching is by name only (a name
// in both panes is "common", regardless of type). Empty unless the local pane is open.
func (m model) commonNames() map[string]bool {
	if !m.localActive {
		return nil
	}
	remote := make(map[string]bool, len(m.entries))
	for _, e := range m.entries {
		remote[e.Name] = true
	}
	common := make(map[string]bool)
	for _, e := range m.localEntries {
		if remote[e.name] {
			common[e.name] = true
		}
	}
	return common
}

// partitionCommon stably reorders entries so unique items keep their (sorted)
// position at the top and common items sink to the bottom — preserving the sort
// order within each group.
func partitionCommon[T any](entries []T, name func(T) string, common map[string]bool) []T {
	if len(common) == 0 {
		return entries
	}
	uniq := make([]T, 0, len(entries))
	var both []T
	for _, e := range entries {
		if common[name(e)] {
			both = append(both, e)
		} else {
			uniq = append(uniq, e)
		}
	}
	return append(uniq, both...)
}

// comparing reports whether comparison ordering/dimming is in effect.
func (m model) comparing() bool { return m.compareMode && m.localActive }

// displayedEntries is the remote listing as shown: filtered by search, and in
// comparison mode reordered so files present in both panes sink to the bottom.
func (m model) displayedEntries() []sshx.Entry {
	es := m.filteredEntries()
	if m.comparing() {
		es = partitionCommon(es, func(e sshx.Entry) string { return e.Name }, m.commonNames())
	}
	return es
}

// displayedLocalEntries is the local listing as shown (filter + comparison order).
func (m model) displayedLocalEntries() []localEntry {
	es := m.filteredLocalEntries()
	if m.comparing() {
		es = partitionCommon(es, func(e localEntry) string { return e.name }, m.commonNames())
	}
	return es
}

// nameCell renders a listing entry's name: a trailing slash for directories,
// colored for directories and muted (darker) when common to both panes in
// comparison mode. The cursor row overrides all of that with the bright style.
func nameCell(name string, isDir, common, cursor bool) string {
	if isDir {
		name += "/"
	}
	switch {
	case cursor:
		return cursorCellStyle(isDir).Render(name)
	case common:
		return mutedStyle.Render(name)
	case isDir:
		return dirStyle.Render(name)
	default:
		return name
	}
}

// sizeCell renders the right-aligned size column: blank for directories, muted
// for common files in comparison mode, dim otherwise. The cursor row is bright.
func sizeCell(isDir, common bool, size int64, cursor bool) string {
	if isDir {
		return fmt.Sprintf("%8s", "")
	}
	s := fmt.Sprintf("%8s", humanSize(size))
	switch {
	case cursor:
		return cursorFileStyle.Render(s) // size only shows for files
	case common:
		return mutedStyle.Render(s)
	default:
		return dimStyle.Render(s)
	}
}

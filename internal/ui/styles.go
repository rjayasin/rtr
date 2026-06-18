package ui

import "github.com/charmbracelet/lipgloss"

// Color palette (256-color safe).
var (
	colAccent = lipgloss.Color("75")  // blue
	colCursor = lipgloss.Color("205") // hot pink, for the cursor marker
	colDim    = lipgloss.Color("244") // gray
	colMuted  = lipgloss.Color("239") // darker gray, for compare-mode common files
	colSel    = lipgloss.Color("220") // yellow
	colOK     = lipgloss.Color("78")  // green
	colErr    = lipgloss.Color("203") // red
	colDir    = lipgloss.Color("75")
	colBright = lipgloss.Color("231") // bright white, for the cursor row's files
	colDirHi  = lipgloss.Color("81")  // bright blue, for the cursor row's directories
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(colAccent).
			Padding(0, 1)

	cursorStyle = lipgloss.NewStyle().Foreground(colCursor).Bold(true)

	// badgeStyle renders the active section's label as a filled block (like the
	// app title bar), so the focused pane reads at a glance.
	badgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(colAccent).
			Padding(0, 1)

	selectedStyle = lipgloss.NewStyle().Foreground(colSel)

	// connStyle renders the connected bookmark label in the bottom-right corner.
	connStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)

	// cursorFileStyle and cursorDirStyle render the cursor row: bold bright white
	// for files, bold bright blue for directories, so the current entry stands out.
	cursorFileStyle = lipgloss.NewStyle().Foreground(colBright).Bold(true)
	cursorDirStyle  = lipgloss.NewStyle().Foreground(colDirHi).Bold(true)

	// dirStyle colors directory names (no bold — bold is reserved for the cursor row).
	dirStyle = lipgloss.NewStyle().Foreground(colDir)

	dimStyle = lipgloss.NewStyle().Foreground(colDim)

	mutedStyle = lipgloss.NewStyle().Foreground(colMuted)

	okStyle = lipgloss.NewStyle().Foreground(colOK).Bold(true)

	errStyle = lipgloss.NewStyle().Foreground(colErr).Bold(true)

	helpStyle = lipgloss.NewStyle().Foreground(colDim)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colDim).
			Padding(0, 1)
)

// cursorCellStyle is the cursor row's style for an entry: bright blue for a
// directory, bright white for a file.
func cursorCellStyle(isDir bool) lipgloss.Style {
	if isDir {
		return cursorDirStyle
	}
	return cursorFileStyle
}

// sectionLabel renders a pane/panel header label: a filled badge when that
// section currently has cursor focus, plain dim "name:" otherwise.
func (m model) sectionLabel(section focusArea, name string) string {
	if m.focus == section {
		return badgeStyle.Render(name)
	}
	return dimStyle.Render(name + ":")
}

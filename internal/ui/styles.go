package ui

import "github.com/charmbracelet/lipgloss"

// Color palette (256-color safe).
var (
	colAccent = lipgloss.Color("75")  // blue
	colDim    = lipgloss.Color("244") // gray
	colMuted  = lipgloss.Color("239") // darker gray, for compare-mode common files
	colSel    = lipgloss.Color("220") // yellow
	colOK     = lipgloss.Color("78")  // green
	colErr    = lipgloss.Color("203") // red
	colDir    = lipgloss.Color("75")
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(colAccent).
			Padding(0, 1)

	pathStyle = lipgloss.NewStyle().Foreground(colDim)

	cursorStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)

	selectedStyle = lipgloss.NewStyle().Foreground(colSel)

	dirStyle = lipgloss.NewStyle().Foreground(colDir).Bold(true)

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

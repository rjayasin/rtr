// Package ui implements rtr's Bubble Tea terminal interface: a bookmarks list, an
// SFTP-backed remote file browser, and a live rsync transfer view. The root model
// is a small screen state machine; blocking work (connect, list, rsync) runs in
// tea.Cmd goroutines that post messages back to Update.
package ui

import (
	"context"
	"os"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/rjayasin/rtr/internal/config"
	"github.com/rjayasin/rtr/internal/sshx"
	"github.com/rjayasin/rtr/internal/transfer"
)

type screen int

const (
	screenBookmarks screen = iota
	screenForm
	screenBrowser
)

type model struct {
	cfg    *config.Config
	screen screen
	width  int
	height int

	// bookmarks
	bmCursor int

	// form (add/edit bookmark)
	form formState

	// browser
	session    *sshx.Session
	cwd        string
	entries    []sshx.Entry
	brCursor   int
	brOffset   int
	selected   map[string]bool
	connecting bool
	spinner    spinner.Model
	sortMode   sortMode
	focus      focusArea // which pane the arrow keys scroll
	xferCursor int       // highlighted transfer when focus is on the panel

	// destination popover (overlaid on the browser)
	destActive     bool
	destInput      textinput.Model
	pendingSources []string
	startDir       string // working dir at launch; default download destination

	// background transfers, shown stacked at the bottom of the browser
	progress  progress.Model
	transfers []*xfer
	nextXfer  int

	status string
	err    error
}

// New builds the initial model.
func New(cfg *config.Config) model {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = dimStyle

	di := textinput.New()
	di.Prompt = "› "
	di.CharLimit = 1024

	// Default download destination: the directory rtr was launched from.
	wd, err := os.Getwd()
	if err != nil {
		wd, _ = os.UserHomeDir()
	}

	return model{
		cfg:       cfg,
		screen:    screenBookmarks,
		selected:  map[string]bool{},
		spinner:   sp,
		destInput: di,
		progress:  progress.New(progress.WithDefaultGradient()),
		startDir:  wd,
	}
}

// ── Messages ────────────────────────────────────────────────────────

type connectedMsg struct {
	session *sshx.Session
	dir     string
	entries []sshx.Entry
}

type listedMsg struct {
	dir     string
	entries []sshx.Entry
}

type startedMsg struct {
	id     int
	ch     <-chan transfer.Event
	cancel context.CancelFunc
}

type evMsg struct {
	id int
	ev transfer.Event
}

type errMsg struct{ err error }

// ── Commands ────────────────────────────────────────────────────────

func connectCmd(b config.Bookmark) tea.Cmd {
	return func() tea.Msg {
		s, err := sshx.Open(b)
		if err != nil {
			return errMsg{err}
		}
		dir := s.Home()
		entries, err := s.List(dir)
		if err != nil {
			s.Close()
			return errMsg{err}
		}
		return connectedMsg{session: s, dir: dir, entries: entries}
	}
}

func listCmd(s *sshx.Session, dir string) tea.Cmd {
	return func() tea.Msg {
		entries, err := s.List(dir)
		if err != nil {
			return errMsg{err}
		}
		return listedMsg{dir: dir, entries: entries}
	}
}

func startCmd(id int, job transfer.Job) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := transfer.Start(ctx, job)
		if err != nil {
			cancel()
			return evMsg{id: id, ev: transfer.Event{Done: true, Err: err}}
		}
		return startedMsg{id: id, ch: ch, cancel: cancel}
	}
}

func waitEvCmd(id int, ch <-chan transfer.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return evMsg{id: id, ev: transfer.Event{Done: true}}
		}
		return evMsg{id: id, ev: ev}
	}
}

// ── tea.Model ───────────────────────────────────────────────────────

func (m model) Init() tea.Cmd { return m.spinner.Tick }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.progress.Width = clamp(msg.Width/3, 16, 36) // compact inline bars
		m.destInput.Width = clamp(msg.Width/2, 10, 60)
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case errMsg:
		m.err = msg.err
		m.connecting = false
		return m, nil

	case connectedMsg:
		m.session = msg.session
		m.cwd = msg.dir
		m.entries = msg.entries
		sortEntries(m.entries, m.sortMode)
		m.brCursor, m.brOffset = 0, 0
		m.connecting = false
		m.err = nil
		m.screen = screenBrowser
		return m, nil

	case listedMsg:
		m.cwd = msg.dir
		m.entries = msg.entries
		sortEntries(m.entries, m.sortMode)
		m.brCursor, m.brOffset = 0, 0
		m.err = nil
		return m, nil

	case startedMsg:
		if x := m.findXfer(msg.id); x != nil {
			x.ch = msg.ch
			x.cancel = msg.cancel
			return m, waitEvCmd(msg.id, msg.ch)
		}
		msg.cancel() // transfer was cleared before it started; stop the process
		return m, nil

	case evMsg:
		return m.handleEvent(msg.id, msg.ev)
	}

	// Screen-specific key handling.
	switch m.screen {
	case screenBookmarks:
		return m.updateBookmarks(msg)
	case screenForm:
		return m.updateForm(msg)
	case screenBrowser:
		return m.updateBrowser(msg)
	}
	return m, nil
}

func (m model) View() string {
	switch m.screen {
	case screenBookmarks:
		return m.viewBookmarks()
	case screenForm:
		return m.viewForm()
	case screenBrowser:
		return m.viewBrowser()
	}
	return ""
}

// Run launches the rtr TUI against the given config and blocks until exit.
func Run(cfg *config.Config) error {
	p := tea.NewProgram(New(cfg), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

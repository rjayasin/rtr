// Package ui implements rtr's Bubble Tea terminal interface: a bookmarks list, an
// SFTP-backed remote file browser, and a live rsync transfer view. The root model
// is a small screen state machine; blocking work (connect, list, rsync) runs in
// tea.Cmd goroutines that post messages back to Update.
package ui

import (
	"context"

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
	screenDest
	screenTransfer
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

	// destination prompt
	destInput      textinput.Model
	pendingSources []string

	// transfer
	progress progress.Model
	job      transfer.Job
	evCh     <-chan transfer.Event
	cancel   context.CancelFunc
	tlog     []string
	tpct     float64
	trate    string
	teta     string
	tbytes   string
	tdone    bool
	terr     error

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

	return model{
		cfg:       cfg,
		screen:    screenBookmarks,
		selected:  map[string]bool{},
		spinner:   sp,
		destInput: di,
		progress:  progress.New(progress.WithDefaultGradient()),
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
	ch     <-chan transfer.Event
	cancel context.CancelFunc
	job    transfer.Job
}

type evMsg struct{ ev transfer.Event }

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

func startCmd(job transfer.Job) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := transfer.Start(ctx, job)
		if err != nil {
			cancel()
			return errMsg{err}
		}
		return startedMsg{ch: ch, cancel: cancel, job: job}
	}
}

func waitEvCmd(ch <-chan transfer.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return evMsg{ev: transfer.Event{Done: true}}
		}
		return evMsg{ev: ev}
	}
}

// ── tea.Model ───────────────────────────────────────────────────────

func (m model) Init() tea.Cmd { return m.spinner.Tick }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.progress.Width = clamp(msg.Width-4, 10, 80)
		m.destInput.Width = clamp(msg.Width-6, 10, 100)
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
		m.brCursor, m.brOffset = 0, 0
		m.connecting = false
		m.err = nil
		m.screen = screenBrowser
		return m, nil

	case listedMsg:
		m.cwd = msg.dir
		m.entries = msg.entries
		m.brCursor, m.brOffset = 0, 0
		m.err = nil
		return m, nil

	case startedMsg:
		m.evCh = msg.ch
		m.cancel = msg.cancel
		m.job = msg.job
		m.tdone = false
		m.terr = nil
		m.tpct = 0
		m.tlog = nil
		return m, waitEvCmd(m.evCh)

	case evMsg:
		return m.handleEvent(msg.ev)
	}

	// Screen-specific key handling.
	switch m.screen {
	case screenBookmarks:
		return m.updateBookmarks(msg)
	case screenForm:
		return m.updateForm(msg)
	case screenBrowser:
		return m.updateBrowser(msg)
	case screenDest:
		return m.updateDest(msg)
	case screenTransfer:
		return m.updateTransfer(msg)
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
	case screenDest:
		return m.viewDest()
	case screenTransfer:
		return m.viewTransfer()
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

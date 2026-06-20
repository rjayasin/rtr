// Package ui implements rtr's Bubble Tea terminal interface: a bookmarks list, an
// SFTP-backed remote file browser, and a live rsync transfer view. The root model
// is a small screen state machine; blocking work (connect, list, rsync) runs in
// tea.Cmd goroutines that post messages back to Update.
package ui

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/rjayasin/rtr/internal/config"
	"github.com/rjayasin/rtr/internal/sshx"
	"github.com/rjayasin/rtr/internal/transfer"
	"github.com/rjayasin/rtr/internal/update"
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
	scrollMem  map[string]int // remembered scroll offset per directory, restored on the way back up
	selected   map[string]bool
	connecting bool
	spinner    spinner.Model
	sortMode   sortMode
	focus      focusArea // which pane the arrow keys scroll
	xferCursor int       // highlighted transfer when focus is on the panel

	// destination popover (overlaid on the browser); shared by downloads (from the
	// remote pane) and uploads (from the local pane, when destUpload is set).
	destActive     bool
	destUpload     bool
	destInput      textinput.Model
	pendingSources []string
	pendingSize    int64  // total recursive size of the sources, once computed
	sizeLoading    bool   // true while the background size walk is running
	sizeReqID      int    // identifies the latest size request (stale results ignored)
	startDir       string // working dir at launch; default download destination

	// browser search (filters the listing by name, case-insensitive substring)
	searchActive bool
	searchInput  textinput.Model

	// showHidden toggles dot-file visibility in both panes (`.`); hidden by default.
	showHidden bool

	// local file pane (toggled with `l`): a read-only view of the local
	// directory rtr was launched from, shown split to the right of the remote list
	localActive       bool
	localCwd          string
	localEntries      []localEntry
	localCursor       int
	localOffset       int
	localErr          error
	localSort         sortMode // defaults to sortTimeDesc (newest first), like the remote pane
	localSearchActive bool
	localSearchInput  textinput.Model
	compareMode       bool // ~ toggles dimming/grouping files present in both panes

	// background transfers, shown stacked at the bottom of every screen
	progress          progress.Model
	transfers         []*xfer
	nextXfer          int
	transfersPath     string // resume file (transfers.json beside the config)
	confirmQuit       bool   // showing the "quit with downloads running?" prompt
	confirmDisconnect bool   // showing the "disconnect from host?" prompt
	disconnectChoice  int    // selected button in the disconnect prompt: 0=Yes, 1=No

	status string
	err    error

	version      string // running build version, for the update check
	updateLatest string // latest published version, set when a newer one exists
}

// displayVersion formats a build version for display: "dev" for source builds,
// otherwise the version with a single leading "v" (GoReleaser injects e.g.
// "0.5.0"; `git describe` already yields "v0.5.0-…").
func displayVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "dev" {
		return "dev"
	}
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// New builds the initial model. version is the running build's version, used for
// the startup "update available" check.
func New(cfg *config.Config, version string) model {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = dimStyle

	di := textinput.New()
	di.Prompt = "› "
	di.CharLimit = 1024

	si := textinput.New()
	si.Prompt = "/"
	si.Placeholder = "filter"
	si.CharLimit = 256

	lsi := textinput.New()
	lsi.Prompt = "/"
	lsi.Placeholder = "filter"
	lsi.CharLimit = 256

	// Default download destination: the directory rtr was launched from.
	wd, err := os.Getwd()
	if err != nil {
		wd, _ = os.UserHomeDir()
	}

	m := model{
		cfg:              cfg,
		screen:           screenBookmarks,
		selected:         map[string]bool{},
		scrollMem:        map[string]int{},
		spinner:          sp,
		destInput:        di,
		searchInput:      si,
		localSearchInput: lsi,
		progress:         progress.New(progress.WithDefaultGradient()),
		startDir:         wd,
		transfersPath:    config.TransfersPath(cfg.Path()),
		version:          version,
	}

	// Restore any transfers that were still running when rtr last exited; they
	// are restarted (and resumed) by Init.
	if pend, err := config.LoadPendingTransfers(m.transfersPath); err == nil {
		for _, p := range pend {
			m.transfers = append(m.transfers, &xfer{
				id:       m.nextXfer,
				label:    transferLabel(p.Sources),
				dest:     p.Dest,
				upload:   p.Upload,
				bookmark: p.Bookmark,
				sources:  p.Sources,
			})
			m.nextXfer++
		}
	}
	return m
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

// dropXferMsg removes a transfer from the panel; it fires on a timer after a
// transfer is cancelled so the cancelled row clears itself.
type dropXferMsg struct{ id int }

// sizeMsg carries the result of a background recursive-size walk for the
// download popover. id matches the request so stale results are discarded.
type sizeMsg struct {
	id   int
	size int64
}

// updateAvailableMsg reports that a newer rtr release exists.
type updateAvailableMsg struct{ latest string }

type errMsg struct{ err error }

// cancelledLinger is how long a cancelled transfer stays visible in the panel
// before it is removed automatically.
const cancelledLinger = 10 * time.Second

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

// sizeCmd walks each source over SFTP and sums their sizes, posting a sizeMsg
// when done. It runs in the background so the popover can render immediately.
func sizeCmd(id int, s *sshx.Session, sources []string) tea.Cmd {
	srcs := append([]string(nil), sources...) // snapshot; caller may mutate
	return func() tea.Msg {
		var total int64
		for _, src := range srcs {
			if n, err := s.PathSize(src); err == nil {
				total += n
			}
		}
		return sizeMsg{id: id, size: total}
	}
}

// localSizeCmd walks each local source and sums their sizes, posting a sizeMsg
// when done — the upload popover's counterpart to sizeCmd, which walks the
// remote over SFTP.
func localSizeCmd(id int, sources []string) tea.Cmd {
	srcs := append([]string(nil), sources...) // snapshot; caller may mutate
	return func() tea.Msg {
		var total int64
		for _, src := range srcs {
			total += localPathSize(src)
		}
		return sizeMsg{id: id, size: total}
	}
}

// localPathSize returns the total size in bytes of a local path: the file size
// for a regular file, or the summed size of every file beneath a directory.
// Unreadable entries are skipped rather than failing the whole walk.
func localPathSize(root string) int64 {
	var total int64
	filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// checkUpdateCmd looks up the latest release in the background and, only if a
// newer version exists, reports it. Failures (offline, etc.) are swallowed so
// the check never disrupts startup; it is skipped when RTR_NO_UPDATE_CHECK is
// set or for non-release builds.
func checkUpdateCmd(version string) tea.Cmd {
	return func() tea.Msg {
		if os.Getenv("RTR_NO_UPDATE_CHECK") != "" {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		latest, newer, err := update.Latest(ctx, version)
		if err != nil || !newer {
			return nil
		}
		return updateAvailableMsg{latest: latest}
	}
}

// dropXferCmd schedules removal of a transfer from the panel after the linger
// window, so cancelled transfers clear themselves without manual intervention.
func dropXferCmd(id int) tea.Cmd {
	return tea.Tick(cancelledLinger, func(time.Time) tea.Msg {
		return dropXferMsg{id: id}
	})
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

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, checkUpdateCmd(m.version)}
	for _, x := range m.transfers { // resume transfers restored in New
		cmds = append(cmds, startCmd(x.id, x.job(m.cfg.Rsync)))
	}
	return tea.Batch(cmds...)
}

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

	case sizeMsg:
		if msg.id == m.sizeReqID {
			m.pendingSize = msg.size
			m.sizeLoading = false
		}
		return m, nil

	case updateAvailableMsg:
		m.updateLatest = msg.latest
		return m, nil

	case errMsg:
		m.err = msg.err
		m.connecting = false
		return m, nil

	case connectedMsg:
		m.session = msg.session
		m.cwd = msg.dir
		m.entries = msg.entries
		m.sortMode = sortTimeDesc // default each connection to newest-first
		sortEntries(m.entries, m.sortMode)
		m.clearSearch()
		m.brCursor, m.brOffset = 0, 0
		m.connecting = false
		m.err = nil
		m.screen = screenBrowser
		return m, nil

	case listedMsg:
		prev := m.cwd
		if prev != msg.dir {
			m.scrollMem[prev] = m.brOffset // remember where we were, to restore on return
		}
		m.cwd = msg.dir
		m.entries = msg.entries
		sortEntries(m.entries, m.sortMode)
		m.clearSearch()
		m.brCursor, m.brOffset = 0, 0
		// When stepping up a level, land the cursor on the directory we came
		// from and restore the scroll position we had in this directory before.
		if path.Dir(prev) == msg.dir && prev != msg.dir {
			child := path.Base(prev)
			for i, e := range m.displayedEntries() {
				if e.Name == child {
					m.brCursor = i
					break
				}
			}
			m.brOffset = m.scrollMem[msg.dir]
			m.clampScroll()
		}
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

	case dropXferMsg:
		m.dropXfer(msg.id)
		m.clampXferCursor()
		if m.focus == focusTransfers && len(m.transfers) == 0 {
			m.focus = focusFiles
		}
		return m, nil
	}

	// Global key handling (quit/confirm and the transfers panel) runs before the
	// screen handlers so it works on any screen.
	if key, ok := msg.(tea.KeyMsg); ok {
		if nm, cmd, handled := m.handleGlobalKey(key); handled {
			return nm, cmd
		}
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

// handleGlobalKey processes keys that apply regardless of the current screen:
// quit/quit-confirmation and the background-transfers panel (focus toggle and
// its actions). It returns handled=false to let the focused screen handle the
// key. It is skipped for text-entry contexts except for Ctrl+C, which always
// requests quit.
func (m model) handleGlobalKey(key tea.KeyMsg) (model, tea.Cmd, bool) {
	ks := key.String()
	textMode := m.screen == screenForm || m.destActive || m.searchActive || m.localSearchActive

	if m.confirmQuit {
		switch ks {
		case "y", "Y":
			m.cancelTransfers()
			return m, tea.Quit, true
		case "n", "N", "esc", "q", "ctrl+c":
			m.confirmQuit = false
			return m, nil, true
		default:
			return m, nil, true // ignore other keys while confirming
		}
	}

	if m.confirmDisconnect {
		switch ks {
		case "left", "right", "tab":
			m.disconnectChoice ^= 1 // toggle between Yes (0) and No (1)
			return m, nil, true
		case "enter":
			if m.disconnectChoice == 0 {
				return m.doDisconnect(), nil, true
			}
			m.confirmDisconnect = false
			return m, nil, true
		case "y", "Y":
			return m.doDisconnect(), nil, true
		case "n", "N", "esc":
			m.confirmDisconnect = false
			return m, nil, true
		default:
			return m, nil, true // modal: ignore other keys while confirming
		}
	}

	if ks == "ctrl+c" || (ks == "q" && !textMode) {
		if m.activeTransfers() > 0 {
			m.confirmQuit = true
			return m, nil, true
		}
		m.cancelTransfers()
		return m, tea.Quit, true
	}

	if textMode {
		return m, nil, false
	}

	if ks == "l" && m.screen == screenBrowser {
		return m.toggleLocal(), nil, true
	}

	if ks == "~" && m.screen == screenBrowser && m.localActive {
		m.compareMode = !m.compareMode
		// Order changes, so restart both cursors at the top.
		m.brCursor, m.brOffset = 0, 0
		m.localCursor, m.localOffset = 0, 0
		return m, nil, true
	}

	if ks == "tab" {
		if cycle := m.focusCycle(); len(cycle) > 1 {
			m.focus = nextInCycle(cycle, m.focus)
			if m.focus == focusTransfers {
				m.clampXferCursor()
			}
			return m, nil, true
		}
	}
	if m.focus == focusTransfers {
		nm, cmd := m.updateTransferFocus(key)
		return nm.(model), cmd, true
	}
	return m, nil, false
}

// focusCycle is the ordered set of panes tab rotates through, given what is
// currently visible: the remote list, the local pane (when open), and the
// transfers panel (when any transfers exist).
func (m model) focusCycle() []focusArea {
	cycle := []focusArea{focusFiles}
	if m.screen == screenBrowser && m.localActive {
		cycle = append(cycle, focusLocal)
	}
	if len(m.transfers) > 0 {
		cycle = append(cycle, focusTransfers)
	}
	return cycle
}

func nextInCycle(cycle []focusArea, cur focusArea) focusArea {
	for i, f := range cycle {
		if f == cur {
			return cycle[(i+1)%len(cycle)]
		}
	}
	return cycle[0]
}

// doDisconnect closes the session and returns to the bookmarks screen. Downloads
// keep running; they show on the bookmarks screen and resume on the next launch.
func (m model) doDisconnect() model {
	m.confirmDisconnect = false
	m.closeSession()
	m.focus = focusFiles
	m.screen = screenBookmarks
	return m
}

func (m model) View() string {
	var v string
	switch m.screen {
	case screenBookmarks:
		v = m.viewBookmarks()
	case screenForm:
		v = m.viewForm()
	case screenBrowser:
		v = m.viewBrowser()
	}
	if m.confirmQuit {
		lines := overlayCenter(strings.Split(v, "\n"), m.quitConfirmBox(), max(m.width, 1))
		v = strings.Join(lines, "\n")
	}
	if m.confirmDisconnect {
		lines := overlayCenter(strings.Split(v, "\n"), m.disconnectConfirmBox(), max(m.width, 1))
		v = strings.Join(lines, "\n")
	}
	return v
}

// Run launches the rtr TUI against the given config and blocks until exit.
func Run(cfg *config.Config, version string) error {
	p := tea.NewProgram(New(cfg, version), tea.WithAltScreen())
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

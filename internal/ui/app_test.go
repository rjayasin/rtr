package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rjayasin/rtr/internal/config"
	"github.com/rjayasin/rtr/internal/sshx"
	"github.com/rjayasin/rtr/internal/transfer"
)

// Cancelling a download removes the partial files it created, but never deletes
// a destination entry that already existed when the transfer started.
func TestCancelCleanup(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(existing, []byte("orig"), 0o644); err != nil {
		t.Fatal(err)
	}

	// At job start, new.bin does not exist yet; keep.txt does.
	remove, globs := cleanupTargets(dir, []string{"/remote/new.bin", "/remote/keep.txt"})
	if len(remove) != 1 || remove[0] != filepath.Join(dir, "new.bin") {
		t.Fatalf("remove = %v, want [<dir>/new.bin]", remove)
	}

	// Simulate rsync writing the partial file and a temp file, then cleanup.
	partial := filepath.Join(dir, "new.bin")
	tmp := filepath.Join(dir, ".new.bin.AbC123")
	for _, p := range []string{partial, tmp} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cleanupPartial(&xfer{cleanupRemove: remove, cleanupGlobs: globs})

	if _, err := os.Stat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Error("partial file should be removed")
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Error("rsync temp file should be removed")
	}
	if _, err := os.Stat(existing); err != nil {
		t.Error("pre-existing file must be preserved")
	}
}

// Pressing c on a highlighted running transfer cancels it and marks it cancelled.
// Routed through Update, since transfer-focus handling is global.
func TestTransferFocusCancel(t *testing.T) {
	m := testModel()
	m.screen = screenBrowser
	m.focus = focusTransfers
	cancelled := false
	m.transfers = []*xfer{{id: 0, label: "f", cancel: func() { cancelled = true }}}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = updated.(model)
	if !cancelled {
		t.Error("cancel func should have been called")
	}
	if !m.transfers[0].cancelled {
		t.Error("transfer should be marked cancelled")
	}
}

// Quitting with a running download asks for confirmation rather than quitting;
// y confirms, n dismisses.
func TestQuitConfirmation(t *testing.T) {
	m := testModel()
	m.screen = screenBrowser
	m.transfers = []*xfer{{id: 0, label: "f", cancel: func() {}}}

	press := func(r rune) tea.Cmd {
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(model)
		return cmd
	}

	// q with an active transfer -> confirmation prompt, no quit command.
	if cmd := press('q'); cmd != nil || !m.confirmQuit {
		t.Fatalf("q should prompt, not quit (confirmQuit=%v, cmd=%v)", m.confirmQuit, cmd)
	}
	// n dismisses.
	press('n')
	if m.confirmQuit {
		t.Error("n should dismiss the prompt")
	}
	// q again, then y -> quit command issued.
	press('q')
	if !m.confirmQuit {
		t.Fatal("q should re-arm the prompt")
	}
	if cmd := press('y'); cmd == nil {
		t.Error("y should issue a quit command")
	}
}

// With no active transfers, q quits immediately (no confirmation).
func TestQuitNoTransfers(t *testing.T) {
	m := testModel()
	m.screen = screenBrowser
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Error("q should quit immediately when nothing is running")
	}
}

// New restores transfers from the resume file; Init restarts them.
func TestResumeRestoresTransfers(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfg, err := config.Load(cfgPath) // creates + sets path
	if err != nil {
		t.Fatal(err)
	}
	pend := []config.PendingTransfer{
		{Bookmark: config.Bookmark{Host: "h", User: "me"}, Sources: []string{"/r/a.txt"}, Dest: dir},
	}
	if err := config.SavePendingTransfers(config.TransfersPath(cfgPath), pend); err != nil {
		t.Fatal(err)
	}

	m := New(cfg)
	if len(m.transfers) != 1 || m.transfers[0].label != "a.txt" {
		t.Fatalf("restored transfers = %+v", m.transfers)
	}
	if cmd := m.Init(); cmd == nil {
		t.Error("Init should return commands to restart resumed transfers")
	}
}

// The popover overlay must keep the base row's text to the left and right of the
// box, so file names beside the popover remain visible.
func TestOverlayLinePreservesSides(t *testing.T) {
	got := ansi.Strip(overlayLine("ABCDEFGH", "XXX", 2, 3))
	if got != "ABXXXFGH" {
		t.Errorf("overlayLine = %q, want %q", got, "ABXXXFGH")
	}

	// A short base row is padded out to the box, then the box is placed.
	got = ansi.Strip(overlayLine("AB", "XXX", 5, 3))
	if got != "AB   XXX" {
		t.Errorf("short row overlay = %q, want %q", got, "AB   XXX")
	}
}

func testModel() model {
	cfg := config.Default()
	cfg.Bookmarks = []config.Bookmark{
		{Name: "nas", User: "me", Host: "nas.local", Port: 2222, RemotePath: "/volume1"},
		{Name: "box", Host: "box"},
	}
	m := New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m2.(model)
}

// Every screen must render without panicking and produce visible output.
func TestViewsRender(t *testing.T) {
	m := testModel()

	m.entries = []sshx.Entry{
		{Name: "dir1", Path: "/volume1/dir1", IsDir: true},
		{Name: "file.txt", Path: "/volume1/file.txt", Size: 4096},
	}
	m.cwd = "/volume1"
	m.form = newForm(m.cfg.Bookmarks[0], 0)

	for _, sc := range []screen{screenBookmarks, screenForm, screenBrowser} {
		m.screen = sc
		if strings.TrimSpace(m.View()) == "" {
			t.Errorf("screen %d rendered empty", sc)
		}
	}

	// Browser with the destination popover open and a background transfer in
	// the bottom panel must still render and mention the file.
	m.screen = screenBrowser
	m.destActive = true
	m.pendingSources = []string{"/volume1/file.txt"}
	m.destInput.SetValue("/tmp")
	m.transfers = []*xfer{{id: 0, label: "file.txt", dest: "/tmp", pct: 42, rate: "1MB/s"}}
	if !strings.Contains(m.View(), "file.txt") {
		t.Error("browser with popover/transfer should mention the file")
	}
}

// 'n' on the bookmarks list opens the new-bookmark form.
func TestBookmarksToForm(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	got := updated.(model)
	if got.screen != screenForm {
		t.Fatalf("screen = %d, want form", got.screen)
	}
	if got.form.editIndex != -1 {
		t.Errorf("editIndex = %d, want -1 (new)", got.form.editIndex)
	}
}

// Directories and files are interspersed; the chosen key orders the listing.
func TestSortEntries(t *testing.T) {
	t0 := time.Unix(1000, 0)
	mk := func(name string, dir bool, size int64, modOffset time.Duration) sshx.Entry {
		return sshx.Entry{Name: name, IsDir: dir, Size: size, ModTime: t0.Add(modOffset)}
	}
	base := []sshx.Entry{
		mk("b.txt", false, 30, 2*time.Hour),
		mk("zdir", true, 0, 5*time.Hour),
		mk("a.txt", false, 10, 3*time.Hour),
		mk("adir", true, 0, 1*time.Hour),
		mk("c.txt", false, 20, 0),
	}
	names := func(es []sshx.Entry) string {
		var n []string
		for _, e := range es {
			n = append(n, e.Name)
		}
		return strings.Join(n, ",")
	}
	clone := func() []sshx.Entry { return append([]sshx.Entry(nil), base...) }

	cases := []struct {
		name string
		mode sortMode
		want string
	}{
		// name: all entries alphabetical, dirs and files interspersed
		// ("a.txt" < "adir" since '.' < 'd').
		{"name", sortName, "a.txt,adir,b.txt,c.txt,zdir"},
		// time: newest→oldest across all entries (zdir t+5, a t+3, b t+2,
		// adir t+1, c t+0).
		{"time", sortTime, "zdir,a.txt,b.txt,adir,c.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			es := clone()
			sortEntries(es, tc.mode)
			if got := names(es); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// 's' toggles the sort mode between name and time.
func TestSortShortcuts(t *testing.T) {
	m := testModel()
	m.screen = screenBrowser
	if m.sortMode != sortName {
		t.Fatalf("initial mode = %v, want name", m.sortMode)
	}
	updated, _ := m.updateBrowser(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = updated.(model)
	if m.sortMode != sortTime {
		t.Errorf("after first s, mode = %v, want time", m.sortMode)
	}
	updated, _ = m.updateBrowser(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = updated.(model)
	if m.sortMode != sortName {
		t.Errorf("after second s, mode = %v, want name", m.sortMode)
	}
}

// Selecting items then pressing d opens the destination popover over the browser
// (without leaving the browser screen) with the right sources.
func TestBrowserDownloadFlow(t *testing.T) {
	m := testModel()
	m.screen = screenBrowser
	m.cwd = "/volume1"
	m.entries = []sshx.Entry{
		{Name: "a.txt", Path: "/volume1/a.txt", Size: 1},
		{Name: "b.txt", Path: "/volume1/b.txt", Size: 2},
	}
	m.brCursor = 1
	// space selects b.txt
	updated, _ := m.updateBrowser(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(model)
	if !m.selected["/volume1/b.txt"] {
		t.Fatal("space did not select current entry")
	}
	// d opens the destination popover (still on the browser screen)
	updated, _ = m.updateBrowser(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = updated.(model)
	if !m.destActive {
		t.Fatal("expected destination popover to be active")
	}
	if m.screen != screenBrowser {
		t.Fatalf("screen = %d, want browser", m.screen)
	}
	if m.destInput.Value() != m.startDir {
		t.Errorf("destination default = %q, want launch dir %q", m.destInput.Value(), m.startDir)
	}
	if len(m.pendingSources) != 1 || m.pendingSources[0] != "/volume1/b.txt" {
		t.Errorf("pendingSources = %v", m.pendingSources)
	}
}

// handleEvent updates the matching background transfer; progress keeps the wait
// loop alive, Done stops it and marks completion.
func TestHandleEvent(t *testing.T) {
	m := testModel()
	m.transfers = []*xfer{{id: 7, label: "f"}}

	p := transfer.Progress{Percent: 42, Rate: "1.2MB/s", ETA: "0:00:05", BytesRaw: "1.2M"}
	updated, cmd := m.handleEvent(7, transfer.Event{Progress: &p})
	m = updated.(model)
	if x := m.findXfer(7); x == nil || x.pct != 42 {
		t.Fatalf("progress not applied: %+v", x)
	}
	if cmd == nil {
		t.Error("expected a follow-up command while transferring")
	}

	updated, cmd = m.handleEvent(7, transfer.Event{Done: true})
	m = updated.(model)
	if x := m.findXfer(7); x == nil || !x.done || x.pct != 100 {
		t.Fatalf("done not applied: %+v", x)
	}
	if cmd != nil {
		t.Error("no follow-up command expected after Done")
	}
}

// Confirming the popover queues a background transfer and returns to browsing.
func TestPopoverEnterQueuesTransfer(t *testing.T) {
	m := testModel()
	m.screen = screenBrowser
	m.session = &sshx.Session{Bookmark: config.Bookmark{Host: "h", User: "me"}}
	m.destActive = true
	m.pendingSources = []string{"/remote/a.txt"}
	m.destInput.SetValue(t.TempDir())

	updated, cmd := m.updateBrowser(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.destActive {
		t.Error("popover should close after enter")
	}
	if len(m.transfers) != 1 || m.transfers[0].label != "a.txt" {
		t.Fatalf("transfers = %+v", m.transfers)
	}
	if cmd == nil {
		t.Error("expected a start command")
	}
}

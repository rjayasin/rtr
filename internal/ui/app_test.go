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

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = updated.(model)
	if !cancelled {
		t.Error("cancel func should have been called")
	}
	if !m.transfers[0].cancelled {
		t.Error("transfer should be marked cancelled")
	}
	if cmd == nil {
		t.Error("cancel should arm the linger timer to remove the row")
	}
}

// After a transfer is cancelled, the linger timer (delivered as a dropXferMsg)
// removes that row from the panel, leaving other transfers untouched and
// returning focus to the file list when the panel empties.
func TestCancelledTransferDropped(t *testing.T) {
	m := testModel()
	m.screen = screenBrowser
	m.focus = focusTransfers
	m.transfers = []*xfer{
		{id: 0, label: "a", cancelled: true},
		{id: 1, label: "b", cancel: func() {}},
	}

	// Dropping the cancelled transfer leaves the still-running one.
	updated, _ := m.Update(dropXferMsg{id: 0})
	m = updated.(model)
	if len(m.transfers) != 1 || m.transfers[0].id != 1 {
		t.Fatalf("transfers = %+v, want only id 1", m.transfers)
	}
	if m.focus != focusTransfers {
		t.Error("focus should stay on the panel while a transfer remains")
	}

	// Dropping the last transfer empties the panel and returns focus to files.
	updated, _ = m.Update(dropXferMsg{id: 1})
	m = updated.(model)
	if len(m.transfers) != 0 {
		t.Fatalf("transfers = %+v, want empty", m.transfers)
	}
	if m.focus != focusFiles {
		t.Error("focus should return to files when the panel empties")
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

	m := New(cfg, "test")
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
	m := New(cfg, "test")
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
		// name ascending: all entries alphabetical, dirs and files interspersed
		// ("a.txt" < "adir" since '.' < 'd').
		{"name asc", sortNameAsc, "a.txt,adir,b.txt,c.txt,zdir"},
		// name descending: reverse alphabetical.
		{"name desc", sortNameDesc, "zdir,c.txt,b.txt,adir,a.txt"},
		// time descending: newest→oldest (zdir t+5, a t+3, b t+2, adir t+1, c t+0).
		{"time desc", sortTimeDesc, "zdir,a.txt,b.txt,adir,c.txt"},
		// time ascending: oldest→newest.
		{"time asc", sortTimeAsc, "c.txt,adir,b.txt,a.txt,zdir"},
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

// 't' selects time sorting and flips newest/oldest on repeat; 'n' selects name
// sorting and flips A→Z / Z→A on repeat. Switching key resets to that key's
// primary direction.
func TestSortShortcuts(t *testing.T) {
	m := testModel()
	m.screen = screenBrowser
	if m.sortMode != sortTimeDesc {
		t.Fatalf("initial mode = %v, want time desc (newest first)", m.sortMode)
	}
	press := func(r string) {
		updated, _ := m.updateBrowser(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)})
		m = updated.(model)
	}

	press("t") // already newest-first → flip to oldest-first
	if m.sortMode != sortTimeAsc {
		t.Errorf("after t, mode = %v, want time asc", m.sortMode)
	}
	press("t") // flip back to newest-first
	if m.sortMode != sortTimeDesc {
		t.Errorf("after second t, mode = %v, want time desc", m.sortMode)
	}
	press("n") // switch to name → A→Z
	if m.sortMode != sortNameAsc {
		t.Errorf("after n, mode = %v, want name asc", m.sortMode)
	}
	press("n") // flip to Z→A
	if m.sortMode != sortNameDesc {
		t.Errorf("after second n, mode = %v, want name desc", m.sortMode)
	}
	press("t") // switch back to time → newest-first
	if m.sortMode != sortTimeDesc {
		t.Errorf("after t from name, mode = %v, want time desc", m.sortMode)
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
	// x selects b.txt
	updated, _ := m.updateBrowser(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = updated.(model)
	if !m.selected["/volume1/b.txt"] {
		t.Fatal("x did not select current entry")
	}
	// enter opens the destination popover (still on the browser screen)
	updated, _ = m.updateBrowser(tea.KeyMsg{Type: tea.KeyEnter})
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

// Enter on a directory with nothing selected opens the download popover for that
// directory rather than navigating into it (→ is used to navigate).
func TestBrowserEnterOnDirDownloads(t *testing.T) {
	m := testModel()
	m.screen = screenBrowser
	m.cwd = "/volume1"
	m.entries = []sshx.Entry{
		{Name: "sub", Path: "/volume1/sub", IsDir: true},
	}
	m.brCursor = 0

	updated, _ := m.updateBrowser(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if !m.destActive {
		t.Fatal("expected destination popover to be active for directory")
	}
	if m.cwd != "/volume1" {
		t.Errorf("cwd = %q, want unchanged /volume1 (enter should not navigate)", m.cwd)
	}
	if len(m.pendingSources) != 1 || m.pendingSources[0] != "/volume1/sub" {
		t.Errorf("pendingSources = %v, want [/volume1/sub]", m.pendingSources)
	}
}

// helper: build a browser model with a fixed listing for search tests.
func browserWithEntries() model {
	m := testModel()
	m.screen = screenBrowser
	m.cwd = "/volume1"
	m.entries = []sshx.Entry{
		{Name: "report.pdf", Path: "/volume1/report.pdf", Size: 10},
		{Name: "Photos", Path: "/volume1/Photos", IsDir: true},
		{Name: "photo-backup.zip", Path: "/volume1/photo-backup.zip", Size: 20},
		{Name: "notes.txt", Path: "/volume1/notes.txt", Size: 30},
	}
	return m
}

// typing into the search field narrows the listing case-insensitively, matching
// the query anywhere in the name.
func TestSearchFiltersEntries(t *testing.T) {
	m := browserWithEntries()

	// '/' opens the search field.
	updated, _ := m.updateBrowser(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updated.(model)
	if !m.searchActive {
		t.Fatal("expected search to be active after /")
	}

	// Typing "photo" matches "Photos" (case-insensitive) and "photo-backup.zip".
	for _, r := range "photo" {
		updated, _ = m.updateBrowser(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(model)
	}
	got := m.filteredEntries()
	if len(got) != 2 {
		t.Fatalf("filtered = %d entries, want 2: %+v", len(got), got)
	}
	names := got[0].Name + "," + got[1].Name
	if names != "Photos,photo-backup.zip" {
		t.Errorf("filtered names = %q, want Photos,photo-backup.zip", names)
	}
}

// enter accepts the filter: search field loses focus but the filter stays
// applied so the list keeps showing matches.
func TestSearchEnterReturnsToList(t *testing.T) {
	m := browserWithEntries()
	updated, _ := m.updateBrowser(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updated.(model)
	for _, r := range "note" {
		updated, _ = m.updateBrowser(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(model)
	}

	updated, _ = m.updateBrowser(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.searchActive {
		t.Error("enter should hand focus back to the list (search inactive)")
	}
	if m.searchInput.Value() != "note" {
		t.Errorf("filter should persist after enter, got %q", m.searchInput.Value())
	}
	if len(m.filteredEntries()) != 1 {
		t.Errorf("filter should still apply: %+v", m.filteredEntries())
	}
}

// esc while searching clears the query and restores the full listing.
func TestSearchEscClears(t *testing.T) {
	m := browserWithEntries()
	updated, _ := m.updateBrowser(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updated.(model)
	for _, r := range "note" {
		updated, _ = m.updateBrowser(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(model)
	}

	updated, _ = m.updateBrowser(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.searchActive {
		t.Error("esc should close the search field")
	}
	if m.searchInput.Value() != "" {
		t.Errorf("esc should clear the query, got %q", m.searchInput.Value())
	}
	if len(m.filteredEntries()) != len(m.entries) {
		t.Errorf("full listing should be restored: %d of %d", len(m.filteredEntries()), len(m.entries))
	}
}

// The status line omits the selection count entirely when nothing is selected.
func TestNoSelectionHidesCount(t *testing.T) {
	m := browserWithEntries()
	if strings.Contains(ansi.Strip(m.View()), "0 selected") {
		t.Error("view should not show '0 selected' when nothing is selected")
	}
	m.selected["/volume1/notes.txt"] = true
	if !strings.Contains(ansi.Strip(m.View()), "1 selected") {
		t.Error("view should show '1 selected' once an entry is selected")
	}
}

// esc in the browser opens a disconnect confirmation; esc/n dismiss it (staying
// connected) and y disconnects to the bookmarks screen.
func TestDisconnectConfirm(t *testing.T) {
	m := testModel()
	m.screen = screenBrowser
	m.cwd = "/volume1"

	// esc opens the prompt without leaving the browser.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if !m.confirmDisconnect {
		t.Fatal("esc should open the disconnect prompt")
	}
	if m.screen != screenBrowser {
		t.Fatalf("should stay on the browser while confirming, got screen %d", m.screen)
	}
	if m.disconnectChoice != 1 {
		t.Errorf("prompt should default to No, got choice %d", m.disconnectChoice)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Disconnect") || !strings.Contains(view, "Yes") || !strings.Contains(view, "No") {
		t.Errorf("disconnect prompt with Yes/No buttons should be visible\n%s", view)
	}

	// esc on the prompt dismisses it, staying connected.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.confirmDisconnect {
		t.Error("esc on the prompt should dismiss it")
	}
	if m.screen != screenBrowser {
		t.Error("dismissing should keep the browser open")
	}

	// n also dismisses.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(model)
	if m.confirmDisconnect || m.screen != screenBrowser {
		t.Error("n should dismiss the prompt and keep the browser open")
	}

	// y disconnects to the bookmarks screen.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(model)
	if m.confirmDisconnect {
		t.Error("y should close the prompt")
	}
	if m.screen != screenBookmarks {
		t.Errorf("y should disconnect to bookmarks, got screen %d", m.screen)
	}
}

// Arrow keys move the selection between Yes/No; enter activates the highlighted
// button. The prompt defaults to No, so an immediate enter stays connected.
func TestDisconnectArrowSelection(t *testing.T) {
	base := testModel()
	base.screen = screenBrowser

	// Default (No) + enter dismisses, staying on the browser.
	updated, _ := base.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m := updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.confirmDisconnect || m.screen != screenBrowser {
		t.Error("enter on the default No should dismiss and stay connected")
	}

	// esc, then ← to select Yes, then enter disconnects.
	updated, _ = base.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(model)
	if m.disconnectChoice != 0 {
		t.Fatalf("← should select Yes, got choice %d", m.disconnectChoice)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.screen != screenBookmarks {
		t.Errorf("enter on Yes should disconnect, got screen %d", m.screen)
	}
}

// An updateAvailableMsg surfaces a notice on the bookmarks screen.
func TestUpdateAvailableNotice(t *testing.T) {
	m := testModel()
	m.screen = screenBookmarks
	if strings.Contains(ansi.Strip(m.View()), "update available") {
		t.Error("no notice should show before an update is detected")
	}
	updated, _ := m.Update(updateAvailableMsg{latest: "v9.9.9"})
	m = updated.(model)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "update available") || !strings.Contains(view, "v9.9.9") {
		t.Errorf("bookmarks view should show the update notice\n%s", view)
	}
}

// `l` opens a local file pane showing the launch directory; tab cycles focus
// between remote and local; → descends into a local subdirectory; `l` closes it.
func TestLocalPane(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := testModel()
	m.screen = screenBrowser
	m.startDir = dir

	// l opens the pane, loads the launch dir, and focuses it.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(model)
	if !m.localActive || m.focus != focusLocal {
		t.Fatalf("l should open and focus the local pane (active=%v focus=%v)", m.localActive, m.focus)
	}
	if m.localCwd != dir || len(m.localEntries) != 2 {
		t.Fatalf("local pane should list %q: cwd=%q entries=%d", dir, m.localCwd, len(m.localEntries))
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "local:") || !strings.Contains(v, "remote:") {
		t.Errorf("split view should show both panes\n%s", v)
	}

	// tab → remote, tab → back to local.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(model)
	if m.focus != focusFiles {
		t.Errorf("tab should move focus to remote, got %v", m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(model)
	if m.focus != focusLocal {
		t.Errorf("tab should move focus back to local, got %v", m.focus)
	}

	// Move the cursor onto "sub" and → descends into it.
	for i, e := range m.localEntries {
		if e.name == "sub" {
			m.localCursor = i
		}
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(model)
	if m.localCwd != filepath.Join(dir, "sub") {
		t.Errorf("→ should descend into sub, got %q", m.localCwd)
	}

	// l closes the pane and returns focus to remote.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(model)
	if m.localActive || m.focus != focusFiles {
		t.Errorf("l should close the pane and focus remote (active=%v focus=%v)", m.localActive, m.focus)
	}
}

// The local pane supports the same t/n sorting as the remote pane: t toggles
// time newest/oldest, n toggles name A→Z / Z→A, defaulting to newest-first.
func TestLocalPaneSorting(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	mk("a.txt", 1*time.Hour) // middle age
	mk("b.txt", 0)           // newest
	mk("c.txt", 2*time.Hour) // oldest

	names := func(m model) string {
		var n []string
		for _, e := range m.localEntries {
			n = append(n, e.name)
		}
		return strings.Join(n, ",")
	}

	m := testModel()
	m.screen = screenBrowser
	m.startDir = dir
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(model)

	// Default: newest first.
	if got := names(m); got != "b.txt,a.txt,c.txt" {
		t.Errorf("default order = %q, want b.txt,a.txt,c.txt", got)
	}
	// t flips to oldest first.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = updated.(model)
	if got := names(m); got != "c.txt,a.txt,b.txt" {
		t.Errorf("after t order = %q, want c.txt,a.txt,b.txt", got)
	}
	// n switches to name A→Z.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(model)
	if got := names(m); got != "a.txt,b.txt,c.txt" {
		t.Errorf("after n order = %q, want a.txt,b.txt,c.txt", got)
	}
	// n again flips to Z→A.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(model)
	if got := names(m); got != "c.txt,b.txt,a.txt" {
		t.Errorf("after second n order = %q, want c.txt,b.txt,a.txt", got)
	}
}

// With the local pane open and navigated into a subdirectory, a new download
// defaults its destination to that local directory rather than the launch dir.
func TestDownloadDestFollowsLocalPane(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := testModel()
	m.screen = screenBrowser
	m.startDir = dir
	m.cwd = "/remote"
	m.entries = []sshx.Entry{{Name: "f.txt", Path: "/remote/f.txt", Size: 1}}
	m.brCursor = 0

	// Open the local pane and descend into downloads/.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(model)
	want := filepath.Join(dir, "downloads")
	if m.localCwd != want {
		t.Fatalf("local cwd = %q, want %q", m.localCwd, want)
	}

	// Back to the remote pane, start a download.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if !m.destActive {
		t.Fatal("enter should open the download popover")
	}
	if got := m.destInput.Value(); got != want {
		t.Errorf("download dest = %q, want local pane dir %q", got, want)
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

// The destination popover lists every selected file by its base name (not the
// full remote path), so a multi-file download shows each file.
func TestDestPopoverListsFilenames(t *testing.T) {
	m := testModel()
	m.pendingSources = []string{"/remote/deep/path/alpha.txt", "/remote/other/beta.log"}
	view := ansi.Strip(m.destPopover())

	for _, want := range []string{"alpha.txt", "beta.log"} {
		if !strings.Contains(view, want) {
			t.Errorf("popover missing %q\n%s", want, view)
		}
	}
	if strings.Contains(view, "/remote/") {
		t.Errorf("popover should show file names, not full paths\n%s", view)
	}
}

// The download popover shows the size inline with the title once it is known,
// and a "calculating…" indicator while the background walk runs.
func TestDestPopoverShowsTotalSize(t *testing.T) {
	m := browserWithEntries()
	m.pendingSources = []string{"/volume1/report.pdf"}

	m.sizeLoading = true
	if loading := ansi.Strip(m.destPopover()); !strings.Contains(loading, "calculating") {
		t.Errorf("popover should show a calculating indicator while loading\n%s", loading)
	}

	m.sizeLoading = false
	m.pendingSize = 1536 // 1.5K
	view := ansi.Strip(m.destPopover())
	if !strings.Contains(view, "Download 1 item • 1.5K") {
		t.Errorf("popover should show size inline with title\n%s", view)
	}
}

// A size result is applied only when its request id is current; stale walks
// (from a popover that was closed/reopened) are ignored.
func TestSizeMsgIgnoresStaleResults(t *testing.T) {
	m := testModel()
	m.sizeReqID = 2
	m.sizeLoading = true

	// Stale result from an earlier request is dropped.
	updated, _ := m.Update(sizeMsg{id: 1, size: 999})
	m = updated.(model)
	if m.pendingSize == 999 || !m.sizeLoading {
		t.Error("stale sizeMsg should be ignored")
	}

	// Current result is applied and clears the loading flag.
	updated, _ = m.Update(sizeMsg{id: 2, size: 4096})
	m = updated.(model)
	if m.pendingSize != 4096 || m.sizeLoading {
		t.Errorf("current sizeMsg not applied: size=%d loading=%v", m.pendingSize, m.sizeLoading)
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

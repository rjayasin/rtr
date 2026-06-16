package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rjayasin/rtr/internal/config"
	"github.com/rjayasin/rtr/internal/sshx"
	"github.com/rjayasin/rtr/internal/transfer"
)

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
	m.pendingSources = []string{"/volume1/file.txt"}
	m.form = newForm(m.cfg.Bookmarks[0], 0)
	m.job = transfer.Job{Sources: m.pendingSources, LocalDest: "/tmp", Cfg: m.cfg.Rsync,
		Bookmark: m.cfg.Bookmarks[0]}

	for _, sc := range []screen{screenBookmarks, screenForm, screenBrowser, screenDest, screenTransfer} {
		m.screen = sc
		out := m.View()
		if strings.TrimSpace(out) == "" {
			t.Errorf("screen %d rendered empty", sc)
		}
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

// A progress event should update percent and keep the loop alive (non-nil cmd);
// a Done event should stop it (nil cmd) and mark completion.
func TestHandleEvent(t *testing.T) {
	m := testModel()
	p := transfer.Progress{Percent: 42, Rate: "1.2MB/s", ETA: "0:00:05", BytesRaw: "1.2M"}
	updated, cmd := m.handleEvent(transfer.Event{Progress: &p})
	m = updated.(model)
	if m.tpct != 42 {
		t.Errorf("tpct = %v, want 42", m.tpct)
	}
	if cmd == nil {
		t.Error("expected a follow-up command while transferring")
	}

	updated, cmd = m.handleEvent(transfer.Event{Done: true})
	m = updated.(model)
	if !m.tdone {
		t.Error("tdone should be true after Done event")
	}
	if cmd != nil {
		t.Error("no follow-up command expected after Done")
	}
	if m.tpct != 100 {
		t.Errorf("tpct = %v, want 100 on success", m.tpct)
	}
}

// Selecting items then choosing download routes to the destination prompt with
// the right sources.
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
	// d opens the destination prompt
	updated, _ = m.updateBrowser(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = updated.(model)
	if m.screen != screenDest {
		t.Fatalf("screen = %d, want dest", m.screen)
	}
	if len(m.pendingSources) != 1 || m.pendingSources[0] != "/volume1/b.txt" {
		t.Errorf("pendingSources = %v", m.pendingSources)
	}
}

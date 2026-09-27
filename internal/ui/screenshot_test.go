package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/rjayasin/rtr/internal/config"
	"github.com/rjayasin/rtr/internal/sshx"
)

// TestScreenshot renders the README screenshot: a browser frame built entirely
// from fabricated hosts, files and transfers (no SSH, no filesystem), written
// as true-color ANSI to $RTR_SCREENSHOT. `make screenshot` turns it into the
// PNG; without the variable this test is skipped.
func TestScreenshot(t *testing.T) {
	out := os.Getenv("RTR_SCREENSHOT")
	if out == "" {
		t.Skip("set RTR_SCREENSHOT=<file> to render the README screenshot")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	now := time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }

	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 104, Height: 28})
	m = updated.(model)
	m.screen = screenBrowser
	m.session = &sshx.Session{Bookmark: config.Bookmark{Name: "nas", User: "me", Host: "nas.local"}}

	m.cwd = "/volume1/media"
	remote := []struct {
		name string
		dir  bool
		size int64
		age  time.Duration
	}{
		{"photos-2026/", true, 0, 2 * time.Hour},
		{"ubuntu-26.04-desktop-amd64.iso", false, 6_120_000_000, 5 * time.Hour},
		{"screen-recording.mp4", false, 4_480_000_000, 9 * time.Hour},
		{"projects/", true, 0, 26 * time.Hour},
		{"vacation-2026.mov", false, 2_310_000_000, 3 * 24 * time.Hour},
		{"invoices-q3.zip", false, 18_400_000, 6 * 24 * time.Hour},
		{"podcast-ep112.flac", false, 612_000_000, 9 * 24 * time.Hour},
		{"notes.md", false, 12_300, 14 * 24 * time.Hour},
		{"backups/", true, 0, 30 * 24 * time.Hour},
		{"family-archive.tar.zst", false, 38_700_000_000, 61 * 24 * time.Hour},
		{"music/", true, 0, 75 * 24 * time.Hour},
		{"thesis-draft-v7.pdf", false, 3_900_000, 90 * 24 * time.Hour},
		{"homelab-compose.yml", false, 6_800, 120 * 24 * time.Hour},
		{"drone-footage-4k.mp4", false, 9_850_000_000, 150 * 24 * time.Hour},
		{"dotfiles.tar.gz", false, 41_200_000, 200 * 24 * time.Hour},
		{"old-phone-backup/", true, 0, 320 * 24 * time.Hour},
		{"recipes.epub", false, 7_300_000, 400 * 24 * time.Hour},
		{"minecraft-world-2025.zip", false, 1_730_000_000, 450 * 24 * time.Hour},
		{"iso/", true, 0, 500 * 24 * time.Hour},
		{"wedding-slideshow.mkv", false, 3_260_000_000, 700 * 24 * time.Hour},
	}
	for _, e := range remote {
		name := e.name
		if e.dir {
			name = name[:len(name)-1]
		}
		m.entries = append(m.entries, sshx.Entry{
			Name: name, Path: m.cwd + "/" + name, IsDir: e.dir, Size: e.size, ModTime: ago(e.age),
		})
	}
	m.selected = map[string]bool{
		m.cwd + "/ubuntu-26.04-desktop-amd64.iso": true,
		m.cwd + "/screen-recording.mp4":           true,
	}
	m.brCursor = 2

	m.localActive = true
	m.localCwd = "~/Downloads"
	m.localEntries = []localEntry{
		{name: "project-backup", isDir: true, modTime: ago(3 * time.Hour)},
		{name: "report-final.pdf", size: 254_000, modTime: ago(20 * time.Hour)},
		{name: "screenshot.png", size: 1_260_000, modTime: ago(2 * 24 * time.Hour)},
		{name: "archlinux-2026.09.01-x86_64.iso", size: 1_420_000_000, modTime: ago(4 * 24 * time.Hour)},
		{name: "keyboard-firmware.hex", size: 88_000, modTime: ago(12 * 24 * time.Hour)},
		{name: "taxes", isDir: true, modTime: ago(40 * 24 * time.Hour)},
		{name: "wallpapers", isDir: true, modTime: ago(55 * 24 * time.Hour)},
		{name: "concert-tickets.pdf", size: 412_000, modTime: ago(70 * 24 * time.Hour)},
		{name: "raspios-arm64.img.xz", size: 1_180_000_000, modTime: ago(95 * 24 * time.Hour)},
	}

	m.transfers = []*xfer{
		{label: "archlinux-2026.09.01-x86_64.iso", pct: 62.4, rate: "18.2MB/s", eta: "0:42"},
		{label: "podcast-ep111.flac", done: true, dest: "~/Downloads", bytes: 598_000_000,
			startedAt: ago(3 * time.Minute), finishedAt: ago(2*time.Minute + 20*time.Second)},
		{label: "site-backup.tar.zst", upload: true, pct: 23.7, rate: "9.1MB/s", eta: "1:55"},
	}

	// The download popover, as enter opens it on the selection above.
	m.pendingSources = []string{
		m.cwd + "/ubuntu-26.04-desktop-amd64.iso",
		m.cwd + "/screen-recording.mp4",
	}
	m.pendingSize = 6_120_000_000 + 4_480_000_000
	m.destInput.SetValue(m.localCwd)
	m.destInput.Focus()
	m.destInput.CursorEnd()
	m.destActive = true

	// freeze ignores reverse video, which is how the input's cursor is drawn,
	// so paint that cell with an explicit light background instead.
	frame := strings.ReplaceAll(m.View(), "\x1b[7m \x1b[0m", "\x1b[48;5;252m \x1b[0m")
	if err := os.WriteFile(out, []byte(frame+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

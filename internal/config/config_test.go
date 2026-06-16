package config

import (
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Default()
	cfg.path = path
	cfg.Rsync.Flags = []string{"-a", "--partial"}
	cfg.Rsync.ExtraArgs = []string{"--exclude", "*.tmp"}
	cfg.Bookmarks = []Bookmark{
		{Name: "nas", User: "me", Host: "nas.local", Port: 2222, RemotePath: "/volume1", Identity: "~/.ssh/id_ed25519"},
		{Name: "box", Host: "box", SSHAlias: "box"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Bookmarks) != 2 {
		t.Fatalf("bookmarks = %d, want 2", len(loaded.Bookmarks))
	}
	b := loaded.Bookmarks[0]
	if b.Name != "nas" || b.Host != "nas.local" || b.Port != 2222 || b.RemotePath != "/volume1" {
		t.Errorf("bookmark[0] mismatch: %+v", b)
	}
	if len(loaded.Rsync.ExtraArgs) != 2 || loaded.Rsync.ExtraArgs[0] != "--exclude" {
		t.Errorf("ExtraArgs = %v", loaded.Rsync.ExtraArgs)
	}
}

func TestLoadMissingCreatesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Rsync.Binary != "rsync" {
		t.Errorf("default binary = %q", cfg.Rsync.Binary)
	}
	// The file should now exist and reload cleanly.
	if _, err := Load(path); err != nil {
		t.Fatalf("reload: %v", err)
	}
}

func TestBookmarkHelpers(t *testing.T) {
	b := Bookmark{Host: "h"}
	if b.EffectivePort() != 22 {
		t.Errorf("default port = %d", b.EffectivePort())
	}
	if b.Target() != "h" {
		t.Errorf("target without user = %q", b.Target())
	}
	b.User = "me"
	if b.Target() != "me@h" {
		t.Errorf("target = %q", b.Target())
	}
	if b.Label() != "me@h" {
		t.Errorf("label fallback = %q", b.Label())
	}
	b.Name = "server"
	if b.Label() != "server" {
		t.Errorf("label = %q", b.Label())
	}
}

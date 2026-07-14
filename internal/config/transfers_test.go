package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPendingTransfersRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transfers.json")
	started := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	want := []PendingTransfer{
		{ID: "k1", Bookmark: Bookmark{Name: "nas", Host: "nas.local", User: "me", Port: 2222},
			Sources: []string{"/a", "/b c"}, Dest: "/dl", PID: 4242, LogPath: "/logs/k1.log", StartedAt: started},
	}
	if err := SavePendingTransfers(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPendingTransfers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Dest != "/dl" || len(got[0].Sources) != 2 || got[0].Bookmark.Port != 2222 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got[0].ID != "k1" || got[0].PID != 4242 || got[0].LogPath != "/logs/k1.log" || !got[0].StartedAt.Equal(started) {
		t.Fatalf("re-attach fields lost in round-trip: %+v", got[0])
	}

	// Saving an empty list removes the file.
	if err := SavePendingTransfers(path, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("empty save should remove the file")
	}

	// Empty path is a no-op (used by in-memory default configs in tests).
	if ts, err := LoadPendingTransfers(""); err != nil || ts != nil {
		t.Errorf("empty path load = %v, %v", ts, err)
	}
}

// A journal written by an older rtr (no id/pid/log fields) still loads; the
// zero PID routes those transfers down the fresh-respawn path.
func TestPendingTransfersLegacyFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transfers.json")
	legacy := `[{"bookmark":{"name":"nas","host":"nas.local","user":"me"},"sources":["/a"],"dest":"/dl"}]`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPendingTransfers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Dest != "/dl" {
		t.Fatalf("legacy journal did not load: %+v", got)
	}
	if got[0].ID != "" || got[0].PID != 0 || got[0].LogPath != "" || !got[0].StartedAt.IsZero() {
		t.Errorf("legacy entry should have zero re-attach fields: %+v", got[0])
	}
}

func TestTransferLogDir(t *testing.T) {
	if got := TransferLogDir("/x/config.toml"); got != filepath.Join("/x", "transfers") {
		t.Errorf("TransferLogDir = %q", got)
	}
	if got := TransferLogDir(""); got != "" {
		t.Errorf("TransferLogDir(\"\") = %q, want \"\"", got)
	}
}

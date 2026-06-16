package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPendingTransfersRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transfers.json")
	want := []PendingTransfer{
		{Bookmark: Bookmark{Name: "nas", Host: "nas.local", User: "me", Port: 2222}, Sources: []string{"/a", "/b c"}, Dest: "/dl"},
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

package transfer

import (
	"strings"
	"testing"

	"github.com/rjayasin/rtr/internal/config"
)

func TestBuildArgs(t *testing.T) {
	job := Job{
		Bookmark:  config.Bookmark{User: "me", Host: "h", Port: 2222, Identity: "/k"},
		Sources:   []string{"/a/b", "/c d"},
		LocalDest: "/local",
		Cfg: config.RsyncConfig{
			Flags:     []string{"-a", "-z"},
			ExtraArgs: []string{"--exclude", ".git"},
		},
	}
	got := BuildArgs(job)
	want := []string{
		"-a", "-z",
		"--info=progress2", "--no-inc-recursive", "-s",
		"--exclude", ".git",
		"-e", "ssh -p 2222 -i /k",
		"me@h:/a/b", "me@h:/c d",
		"/local",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("BuildArgs()\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildArgsDefaultsAndNoTransport(t *testing.T) {
	// Port 22 with no identity/jump => no -e transport, default flags applied.
	job := Job{
		Bookmark:  config.Bookmark{User: "me", Host: "h"},
		Sources:   []string{"/data"},
		LocalDest: ".",
	}
	got := strings.Join(BuildArgs(job), " ")
	if strings.Contains(got, "-e ") {
		t.Errorf("expected no -e transport, got %q", got)
	}
	for _, want := range []string{"-a", "--info=progress2", "--no-inc-recursive", "-s", "me@h:/data"} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q missing %q", got, want)
		}
	}
}

// An upload reverses the argument order: local sources first, then the remote
// destination as the final host:path argument.
func TestBuildArgsUpload(t *testing.T) {
	job := Job{
		Bookmark:   config.Bookmark{User: "me", Host: "h", Port: 2222, Identity: "/k"},
		Sources:    []string{"/local/a", "/local/b c"},
		RemoteDest: "/remote/dir",
		Upload:     true,
		Cfg:        config.RsyncConfig{Flags: []string{"-a", "-z"}},
	}
	got := BuildArgs(job)
	want := []string{
		"-a", "-z",
		"--info=progress2", "--no-inc-recursive", "-s",
		"-e", "ssh -p 2222 -i /k",
		"/local/a", "/local/b c",
		"me@h:/remote/dir",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("BuildArgs() upload\n got: %q\nwant: %q", got, want)
	}
}

// Remote paths are passed verbatim (no shell quoting), since rtr execs rsync
// directly and -s protects them from the remote shell.
func TestBuildArgsRemotePathVerbatim(t *testing.T) {
	job := Job{
		Bookmark:  config.Bookmark{User: "me", Host: "h"},
		Sources:   []string{"/home/me/a file.txt"},
		LocalDest: ".",
	}
	got := BuildArgs(job)
	found := false
	for _, a := range got {
		if a == "me@h:/home/me/a file.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected verbatim remote path in %q", got)
	}
}

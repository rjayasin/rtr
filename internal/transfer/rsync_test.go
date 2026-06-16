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
		"--info=progress2", "--no-inc-recursive",
		"--exclude", ".git",
		"-e", "ssh -p 2222 -i /k",
		"me@h:'/a/b'", "me@h:'/c d'",
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
	for _, want := range []string{"-a", "--info=progress2", "--no-inc-recursive", "me@h:'/data'"} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q missing %q", got, want)
		}
	}
}

func TestQuoteRemote(t *testing.T) {
	if got := quoteRemote("/a/b"); got != "'/a/b'" {
		t.Errorf("quoteRemote(/a/b) = %q", got)
	}
	if got := quoteRemote("/a'b"); got != `'/a'\''b'` {
		t.Errorf("quoteRemote with apostrophe = %q", got)
	}
}

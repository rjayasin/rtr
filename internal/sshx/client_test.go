package sshx

import (
	"testing"

	"github.com/kevinburke/ssh_config"
	"github.com/rjayasin/rtr/internal/config"
)

// resolveAlias must not adopt ssh_config's *default* IdentityFile
// ("~/.ssh/identity") for a host that has no explicit entry — doing so used to
// suppress the default-key scan in authMethods and leave hosts with no agent
// unable to authenticate.
func TestResolveAliasIgnoresDefaultIdentity(t *testing.T) {
	b := resolveAlias(config.Bookmark{Host: "no-such-host.invalid.example", User: "x"})
	if b.Identity == ssh_config.Default("IdentityFile") {
		t.Errorf("Identity adopted the ssh_config default %q", b.Identity)
	}
	if b.Identity != "" {
		t.Logf("Identity resolved from local ssh_config: %q", b.Identity)
	}
}

// parseJump splits a ProxyJump spec into user, host, and port, leaving the port
// unset (so EffectivePort defaults to 22) when none is given.
func TestParseJump(t *testing.T) {
	cases := []struct {
		spec    string
		user    string
		host    string
		port    int // 0 means unspecified
		effPort int
	}{
		{"user@bastion:2222", "user", "bastion", 2222, 2222},
		{"bastion", "", "bastion", 0, 22},
		{"bastion:22", "", "bastion", 22, 22},
		{"me@host", "me", "host", 0, 22},
		{"me@[::1]:2222", "me", "::1", 2222, 2222}, // IPv6 literal
	}
	for _, tc := range cases {
		b := parseJump(tc.spec)
		if b.User != tc.user || b.Host != tc.host || b.Port != tc.port {
			t.Errorf("parseJump(%q) = {User:%q Host:%q Port:%d}, want {User:%q Host:%q Port:%d}",
				tc.spec, b.User, b.Host, b.Port, tc.user, tc.host, tc.port)
		}
		if got := b.EffectivePort(); got != tc.effPort {
			t.Errorf("parseJump(%q).EffectivePort() = %d, want %d", tc.spec, got, tc.effPort)
		}
	}
}

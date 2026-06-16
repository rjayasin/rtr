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

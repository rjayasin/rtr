// Package sshx wraps SSH dialing and SFTP directory browsing for rtr. It mirrors
// the auth conventions of the user's existing tooling: prefer ssh-agent, fall
// back to an explicit identity file, honor ~/.ssh/config aliases, and verify host
// keys against ~/.ssh/known_hosts with accept-on-first-use semantics.
package sshx

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kevinburke/ssh_config"
	"github.com/rjayasin/rtr/internal/config"
	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const dialTimeout = 20 * time.Second

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return homeDir() + p[1:]
	}
	return p
}

// resolveAlias fills empty bookmark fields from a matching ~/.ssh/config Host.
func resolveAlias(b config.Bookmark) config.Bookmark {
	alias := b.SSHAlias
	if alias == "" {
		alias = b.Host
	}
	if alias == "" {
		return b
	}
	if b.Host == "" || b.SSHAlias != "" {
		if hn, _ := ssh_config.GetStrict(alias, "HostName"); hn != "" {
			b.Host = hn
		}
	}
	if b.User == "" {
		if u, _ := ssh_config.GetStrict(alias, "User"); u != "" {
			b.User = u
		}
	}
	if b.Port == 0 {
		if ps, _ := ssh_config.GetStrict(alias, "Port"); ps != "" {
			if p, err := strconv.Atoi(ps); err == nil {
				b.Port = p
			}
		}
	}
	if b.Identity == "" {
		// GetStrict returns the library default ("~/.ssh/identity") when the host
		// has no explicit IdentityFile; ignore that so the default-key scan in
		// authMethods still runs.
		if id, _ := ssh_config.GetStrict(alias, "IdentityFile"); id != "" && id != ssh_config.Default("IdentityFile") {
			b.Identity = id
		}
	}
	if b.Host == "" {
		b.Host = alias
	}
	return b
}

func keyAuth(path string) (ssh.AuthMethod, error) {
	data, err := os.ReadFile(expandHome(path))
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		// Encrypted keys without a passphrase prompt are left to the agent.
		return nil, err
	}
	return ssh.PublicKeys(signer), nil
}

// authMethods assembles the SSH auth methods for a bookmark. The returned
// cleanup closes the ssh-agent socket connection (a no-op when no agent is
// used); callers must invoke it once the handshake has completed, since the
// agent is only consulted during authentication.
func authMethods(b config.Bookmark) (methods []ssh.AuthMethod, cleanup func()) {
	cleanup = func() {}
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			cleanup = func() { conn.Close() }
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}
	seen := map[string]bool{}
	addKey := func(path string) {
		p := expandHome(path)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		if _, err := os.Stat(p); err != nil {
			return
		}
		if m, err := keyAuth(p); err == nil {
			methods = append(methods, m)
		}
	}
	// Try an explicitly configured identity first, then always fall back to the
	// usual default key names (so a missing/bogus identity never leaves us with
	// no methods when an unencrypted default key is present).
	addKey(b.Identity)
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		addKey(filepath.Join(homeDir(), ".ssh", name))
	}
	return methods, cleanup
}

// hostKeyConfig builds the host-key verification callback and the list of host
// key algorithms already pinned for addr. An unknown host is trusted on first
// use (its key is appended), matching OpenSSH's StrictHostKeyChecking=accept-new;
// a *changed* key is rejected. Returning the pinned algorithms lets the client
// prefer the same key type already in known_hosts — without this, x/crypto picks
// from its own global order (RSA before ed25519) and falsely reports a changed
// key when only a different type was pinned (e.g. by OpenSSH).
func hostKeyConfig(path, addr string) (ssh.HostKeyCallback, []string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		f.Close()
	}
	db, err := knownhosts.NewDB(path)
	if err != nil {
		return nil, nil, err
	}
	verify := db.HostKeyCallback()
	cb := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		switch err := verify(hostname, remote, key); {
		case err == nil:
			return nil
		case knownhosts.IsHostKeyChanged(err):
			return fmt.Errorf("REMOTE HOST KEY CHANGED for %s — possible attack; "+
				"fix ~/.ssh/known_hosts manually if expected", hostname)
		case knownhosts.IsHostUnknown(err):
			return appendKnownHost(path, hostname, remote, key)
		default:
			return err
		}
	}
	// Empty for an unknown host, in which case x/crypto falls back to its default
	// algorithm order (fine for trust-on-first-use).
	return cb, db.HostKeyAlgorithms(addr), nil
}

func appendKnownHost(path, hostname string, remote net.Addr, key ssh.PublicKey) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return knownhosts.WriteKnownHost(f, hostname, remote, key)
}

// clientConfig builds the SSH client config. The returned cleanup releases the
// ssh-agent connection backing the auth methods and must be called once the
// handshake using this config has finished.
func clientConfig(b config.Bookmark) (*ssh.ClientConfig, func(), error) {
	addr := net.JoinHostPort(b.Host, strconv.Itoa(b.EffectivePort()))
	cb, hostKeyAlgos, err := hostKeyConfig(filepath.Join(homeDir(), ".ssh", "known_hosts"), addr)
	if err != nil {
		return nil, func() {}, err
	}
	user := b.User
	if user == "" {
		user = os.Getenv("USER")
	}
	methods, cleanup := authMethods(b)
	return &ssh.ClientConfig{
		User:              user,
		Auth:              methods,
		HostKeyCallback:   cb,
		HostKeyAlgorithms: hostKeyAlgos,
		Timeout:           dialTimeout,
	}, cleanup, nil
}

// Dial opens an SSH connection for the bookmark, transparently routing through a
// jump host when one is configured.
func Dial(b config.Bookmark) (*ssh.Client, error) {
	b = resolveAlias(b)
	if b.Host == "" {
		return nil, errors.New("bookmark has no host")
	}
	cfg, cleanup, err := clientConfig(b)
	if err != nil {
		return nil, err
	}
	// The agent is only needed during the handshake, which ssh.Dial /
	// NewClientConn complete before this function returns; release it after.
	defer cleanup()
	addr := net.JoinHostPort(b.Host, strconv.Itoa(b.EffectivePort()))

	if b.JumpHost == "" {
		return ssh.Dial("tcp", addr, cfg)
	}

	jb := parseJump(b.JumpHost)
	jcfg, jcleanup, err := clientConfig(jb)
	if err != nil {
		return nil, err
	}
	defer jcleanup()
	jclient, err := ssh.Dial("tcp", net.JoinHostPort(jb.Host, strconv.Itoa(jb.EffectivePort())), jcfg)
	if err != nil {
		return nil, fmt.Errorf("jump host %s: %w", b.JumpHost, err)
	}
	conn, err := jclient.Dial("tcp", addr)
	if err != nil {
		jclient.Close()
		return nil, fmt.Errorf("dial %s via jump: %w", addr, err)
	}
	ncc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		jclient.Close()
		return nil, err
	}
	return ssh.NewClient(ncc, chans, reqs), nil
}

// parseJump parses a ProxyJump spec like "user@bastion:2222".
func parseJump(spec string) config.Bookmark {
	var b config.Bookmark
	if at := strings.LastIndex(spec, "@"); at >= 0 {
		b.User = spec[:at]
		spec = spec[at+1:]
	}
	if h, p, err := net.SplitHostPort(spec); err == nil {
		b.Host = h
		if n, err := strconv.Atoi(p); err == nil {
			b.Port = n
		}
	} else {
		b.Host = spec
	}
	return b
}

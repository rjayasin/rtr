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
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
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
		if id, _ := ssh_config.GetStrict(alias, "IdentityFile"); id != "" {
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

func authMethods(b config.Bookmark) []ssh.AuthMethod {
	var methods []ssh.AuthMethod
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}
	if b.Identity != "" {
		if m, err := keyAuth(b.Identity); err == nil {
			methods = append(methods, m)
		}
	} else {
		for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			p := filepath.Join(homeDir(), ".ssh", name)
			if _, err := os.Stat(p); err == nil {
				if m, err := keyAuth(p); err == nil {
					methods = append(methods, m)
				}
			}
		}
	}
	return methods
}

// hostKeyCallback verifies against known_hosts. An unknown host is trusted on
// first use (its key is appended), matching OpenSSH's StrictHostKeyChecking=
// accept-new. A *changed* key is rejected, since that is the dangerous case.
func hostKeyCallback(path string) (ssh.HostKeyCallback, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		f.Close()
	}
	known, err := knownhosts.New(path)
	if err != nil {
		return nil, err
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := known(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) {
			if len(keyErr.Want) > 0 {
				return fmt.Errorf("REMOTE HOST KEY CHANGED for %s — possible attack; "+
					"fix ~/.ssh/known_hosts manually if expected", hostname)
			}
			return appendKnownHost(path, hostname, key)
		}
		return err
	}, nil
}

func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	_, err = f.WriteString(line + "\n")
	return err
}

func clientConfig(b config.Bookmark) (*ssh.ClientConfig, error) {
	cb, err := hostKeyCallback(filepath.Join(homeDir(), ".ssh", "known_hosts"))
	if err != nil {
		return nil, err
	}
	user := b.User
	if user == "" {
		user = os.Getenv("USER")
	}
	return &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods(b),
		HostKeyCallback: cb,
		Timeout:         dialTimeout,
	}, nil
}

// Dial opens an SSH connection for the bookmark, transparently routing through a
// jump host when one is configured.
func Dial(b config.Bookmark) (*ssh.Client, error) {
	b = resolveAlias(b)
	if b.Host == "" {
		return nil, errors.New("bookmark has no host")
	}
	cfg, err := clientConfig(b)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(b.Host, strconv.Itoa(b.EffectivePort()))

	if b.JumpHost == "" {
		return ssh.Dial("tcp", addr, cfg)
	}

	jb := parseJump(b.JumpHost)
	jcfg, err := clientConfig(jb)
	if err != nil {
		return nil, err
	}
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

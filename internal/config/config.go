// Package config defines rtr's persisted configuration: bookmarked remote SSH
// locations and the rsync settings used to pull files from them. The file lives
// at $XDG_CONFIG_HOME/rtr/config.toml (falling back to ~/.config/rtr/config.toml)
// and is created with sensible defaults on first run.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Bookmark is a saved remote SSH location to browse and download from.
type Bookmark struct {
	Name       string `toml:"name"`
	Host       string `toml:"host"`
	User       string `toml:"user"`
	Port       int    `toml:"port"`
	RemotePath string `toml:"remote_path"` // starting directory when browsing
	Identity   string `toml:"identity"`    // private key path (optional; ~ expanded)
	JumpHost   string `toml:"jump_host"`   // optional ProxyJump, e.g. user@bastion:22
	SSHAlias   string `toml:"ssh_alias"`   // optional ~/.ssh/config Host to inherit from
}

// EffectivePort returns the SSH port, defaulting to 22.
func (b Bookmark) EffectivePort() int {
	if b.Port == 0 {
		return 22
	}
	return b.Port
}

// Target returns the user@host form used in rsync sources and ssh targets.
func (b Bookmark) Target() string {
	if b.User == "" {
		return b.Host
	}
	return b.User + "@" + b.Host
}

// Label is the display name for a bookmark, falling back to its target.
func (b Bookmark) Label() string {
	if strings.TrimSpace(b.Name) != "" {
		return b.Name
	}
	return b.Target()
}

// RsyncConfig controls how the rsync command line is assembled. Sources and the
// SSH transport (-e ssh ...) are derived from the selected bookmark; these are
// the user-tunable pieces.
type RsyncConfig struct {
	Binary    string   `toml:"binary"`     // rsync executable (default "rsync")
	Flags     []string `toml:"flags"`      // base flags, e.g. -a -z --partial
	ExtraArgs []string `toml:"extra_args"` // appended verbatim before src/dst
}

// Config is the whole persisted file.
type Config struct {
	DefaultLocalDir string      `toml:"default_local_dir"`
	Rsync           RsyncConfig `toml:"rsync"`
	Bookmarks       []Bookmark  `toml:"bookmarks"`

	path string // where this was loaded from; not serialized
}

// Path returns the file this config was loaded from / will be saved to.
func (c *Config) Path() string { return c.path }

// DefaultPath resolves the standard config file location.
func DefaultPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "rtr", "config.toml"), nil
}

// Default returns a Config populated with first-run defaults.
func Default() *Config {
	home, _ := os.UserHomeDir()
	dl := home
	if dl == "" {
		dl = "."
	}
	return &Config{
		DefaultLocalDir: dl,
		Rsync: RsyncConfig{
			Binary: "rsync",
			Flags:  []string{"-a", "-z", "--partial", "--human-readable"},
		},
	}
}

// Load reads the config at path (or DefaultPath if empty). A missing file is not
// an error: defaults are returned and written to disk so the user has something
// to edit.
func Load(path string) (*Config, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	cfg := Default()
	cfg.path = path

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, cfg.Save()
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Rsync.Binary == "" {
		c.Rsync.Binary = "rsync"
	}
	if len(c.Rsync.Flags) == 0 {
		c.Rsync.Flags = []string{"-a", "-z", "--partial", "--human-readable"}
	}
	if c.DefaultLocalDir == "" {
		c.DefaultLocalDir, _ = os.UserHomeDir()
	}
}

// Save writes the config back to its path, creating parent directories.
func (c *Config) Save() error {
	if c.path == "" {
		p, err := DefaultPath()
		if err != nil {
			return err
		}
		c.path = p
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := toml.NewEncoder(f)
	if err := enc.Encode(c); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, c.path) // atomic replace
}

// Upsert inserts a new bookmark or replaces the one at index i (i < 0 to add).
func (c *Config) Upsert(i int, b Bookmark) {
	if i < 0 || i >= len(c.Bookmarks) {
		c.Bookmarks = append(c.Bookmarks, b)
		return
	}
	c.Bookmarks[i] = b
}

// Remove deletes the bookmark at index i if in range.
func (c *Config) Remove(i int) {
	if i < 0 || i >= len(c.Bookmarks) {
		return
	}
	c.Bookmarks = append(c.Bookmarks[:i], c.Bookmarks[i+1:]...)
}

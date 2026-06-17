package sshx

import (
	"io/fs"
	"path"
	"sort"
	"time"

	"github.com/pkg/sftp"
	"github.com/rjayasin/rtr/internal/config"
	"golang.org/x/crypto/ssh"
)

// Entry is a single item in a remote directory listing.
type Entry struct {
	Name    string
	Path    string // absolute remote path
	IsDir   bool
	Size    int64
	Mode    fs.FileMode
	ModTime time.Time
}

// Session is a live SSH+SFTP connection used to browse a remote host.
type Session struct {
	Bookmark config.Bookmark
	client   *ssh.Client
	sftp     *sftp.Client
}

// Open dials the bookmark and starts an SFTP subsystem.
func Open(b config.Bookmark) (*Session, error) {
	client, err := Dial(b)
	if err != nil {
		return nil, err
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, err
	}
	return &Session{Bookmark: b, client: client, sftp: sc}, nil
}

// Home returns a sensible starting directory: the bookmark's RemotePath if set,
// otherwise the remote working directory (the login home).
func (s *Session) Home() string {
	if s.Bookmark.RemotePath != "" {
		return s.Bookmark.RemotePath
	}
	if wd, err := s.sftp.Getwd(); err == nil && wd != "" {
		return wd
	}
	return "/"
}

// List returns the contents of dir, directories first then files, each sorted
// case-insensitively by name.
func (s *Session) List(dir string) ([]Entry, error) {
	infos, err := s.sftp.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(infos))
	for _, fi := range infos {
		isDir := fi.IsDir()
		// Resolve symlinks so the user can descend into linked directories.
		if fi.Mode()&fs.ModeSymlink != 0 {
			if st, err := s.sftp.Stat(path.Join(dir, fi.Name())); err == nil {
				isDir = st.IsDir()
			}
		}
		entries = append(entries, Entry{
			Name:    fi.Name(),
			Path:    path.Join(dir, fi.Name()),
			IsDir:   isDir,
			Size:    fi.Size(),
			Mode:    fi.Mode(),
			ModTime: fi.ModTime(),
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return lessFold(entries[i].Name, entries[j].Name)
	})
	return entries, nil
}

// PathSize returns the total size in bytes of a remote path: the file size for
// a regular file, or the summed size of every file beneath it for a directory.
// Unreadable entries are skipped rather than failing the whole walk.
func (s *Session) PathSize(root string) (int64, error) {
	var total int64
	w := s.sftp.Walk(root)
	for w.Step() {
		if w.Err() != nil {
			continue
		}
		if fi := w.Stat(); !fi.IsDir() {
			total += fi.Size()
		}
	}
	return total, nil
}

// Close tears down the SFTP and SSH connections.
func (s *Session) Close() error {
	if s.sftp != nil {
		s.sftp.Close()
	}
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

func lessFold(a, b string) bool {
	la, lb := toLower(a), toLower(b)
	if la == lb {
		return a < b
	}
	return la < lb
}

func toLower(s string) string {
	bs := []byte(s)
	for i, c := range bs {
		if c >= 'A' && c <= 'Z' {
			bs[i] = c + ('a' - 'A')
		}
	}
	return string(bs)
}

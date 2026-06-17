package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/rjayasin/rtr/internal/config"
)

// ── Bookmarks list ──────────────────────────────────────────────────

func (m model) updateBookmarks(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		if m.bmCursor > 0 {
			m.bmCursor--
		}
	case "down", "j":
		if m.bmCursor < len(m.cfg.Bookmarks)-1 {
			m.bmCursor++
		}
	case "n":
		m.form = newForm(config.Bookmark{Port: 22}, -1)
		m.err = nil
		m.screen = screenForm
	case "e":
		if len(m.cfg.Bookmarks) > 0 {
			m.form = newForm(m.cfg.Bookmarks[m.bmCursor], m.bmCursor)
			m.err = nil
			m.screen = screenForm
		}
	case "d":
		if len(m.cfg.Bookmarks) > 0 {
			m.cfg.Remove(m.bmCursor)
			if m.bmCursor >= len(m.cfg.Bookmarks) && m.bmCursor > 0 {
				m.bmCursor--
			}
			if err := m.cfg.Save(); err != nil {
				m.err = err
			}
		}
	case "enter":
		if len(m.cfg.Bookmarks) > 0 {
			m.connecting = true
			m.err = nil
			b := m.cfg.Bookmarks[m.bmCursor]
			m.status = "connecting to " + b.Label()
			return m, connectCmd(b)
		}
	}
	return m, nil
}

func (m model) viewBookmarks() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("rtr — bookmarks"))

	if len(m.cfg.Bookmarks) == 0 {
		fmt.Fprintf(&b, "%s\n", dimStyle.Render("No bookmarks yet. Press n to add one."))
	}
	for i, bm := range m.cfg.Bookmarks {
		cursor := "  "
		line := fmt.Sprintf("%-22s %s", bm.Label(), dimStyle.Render(bm.Target()+":"+orDefault(bm.RemotePath, "~")))
		if i == m.bmCursor && m.focus == focusFiles {
			cursor = cursorStyle.Render("▸ ")
			line = cursorStyle.Render(fmt.Sprintf("%-22s ", bm.Label())) + dimStyle.Render(bm.Target()+":"+orDefault(bm.RemotePath, "~"))
		}
		fmt.Fprintf(&b, "%s%s\n", cursor, line)
	}

	b.WriteString("\n")
	if m.connecting {
		fmt.Fprintf(&b, "%s %s\n", m.spinner.View(), m.status)
	}
	if m.err != nil {
		fmt.Fprintf(&b, "%s%s\n", errStyle.Render("error: "), m.err.Error())
	}
	if panel := m.transfersView(); panel != "" {
		fmt.Fprintf(&b, "%s\n%s\n", dividerLine(m.width), panel)
	}
	fmt.Fprintf(&b, "\n%s", helpStyle.Render(m.footer(helpBookmarks)))
	return b.String()
}

// ── Add / edit form ─────────────────────────────────────────────────

var formLabels = []string{
	"Name", "User", "Host", "Port", "Remote path", "Identity file", "Jump host", "ssh_config alias",
}

type formState struct {
	inputs    []textinput.Model
	focus     int
	editIndex int // -1 = creating a new bookmark
}

func newForm(b config.Bookmark, editIndex int) formState {
	vals := []string{
		b.Name, b.User, b.Host, portStr(b.Port), b.RemotePath, b.Identity, b.JumpHost, b.SSHAlias,
	}
	placeholders := []string{
		"my server", "user", "host.example.com", "22", "~ or /path", "~/.ssh/id_ed25519", "user@bastion", "config Host",
	}
	fs := formState{editIndex: editIndex}
	for i := range formLabels {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 256
		ti.Width = 40
		ti.SetValue(vals[i])
		ti.Placeholder = placeholders[i]
		if i == 0 {
			ti.Focus()
		}
		fs.inputs = append(fs.inputs, ti)
	}
	return fs
}

func (m model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "esc":
		m.screen = screenBookmarks
		return m, nil
	case "enter":
		return m.saveForm()
	case "tab", "down":
		m.form.focusNext(1)
		return m, nil
	case "shift+tab", "up":
		m.form.focusNext(-1)
		return m, nil
	}

	var cmd tea.Cmd
	m.form.inputs[m.form.focus], cmd = m.form.inputs[m.form.focus].Update(msg)
	return m, cmd
}

func (fs *formState) focusNext(dir int) {
	fs.inputs[fs.focus].Blur()
	fs.focus = (fs.focus + dir + len(fs.inputs)) % len(fs.inputs)
	fs.inputs[fs.focus].Focus()
}

func (m model) saveForm() (tea.Model, tea.Cmd) {
	get := func(i int) string { return strings.TrimSpace(m.form.inputs[i].Value()) }
	port, _ := strconv.Atoi(get(3))
	b := config.Bookmark{
		Name:       get(0),
		User:       get(1),
		Host:       get(2),
		Port:       port,
		RemotePath: get(4),
		Identity:   get(5),
		JumpHost:   get(6),
		SSHAlias:   get(7),
	}
	if b.Host == "" && b.SSHAlias == "" {
		m.err = fmt.Errorf("host or ssh_config alias is required")
		return m, nil
	}
	m.cfg.Upsert(m.form.editIndex, b)
	if err := m.cfg.Save(); err != nil {
		m.err = err
		return m, nil
	}
	if m.form.editIndex < 0 {
		m.bmCursor = len(m.cfg.Bookmarks) - 1
	}
	m.err = nil
	m.screen = screenBookmarks
	return m, nil
}

func (m model) viewForm() string {
	var b strings.Builder
	title := "new bookmark"
	if m.form.editIndex >= 0 {
		title = "edit bookmark"
	}
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render("rtr — "+title))
	for i, label := range formLabels {
		marker := "  "
		if i == m.form.focus {
			marker = cursorStyle.Render("▸ ")
		}
		fmt.Fprintf(&b, "%s%-16s %s\n", marker, label+":", m.form.inputs[i].View())
	}
	b.WriteString("\n")
	if m.err != nil {
		fmt.Fprintf(&b, "%s%s\n\n", errStyle.Render("error: "), m.err.Error())
	}
	b.WriteString(helpStyle.Render(helpForm))
	return b.String()
}

func portStr(p int) string {
	if p == 0 {
		return ""
	}
	return strconv.Itoa(p)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

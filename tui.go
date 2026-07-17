package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type screen int

const (
	menuScreen screen = iota
	sessionFormScreen
	groupFormScreen
	settingsScreen
	confirmScreen
)

type rowKind int

const (
	groupRow rowKind = iota
	sessionRow
	actionRow
)

type menuRow struct {
	kind  rowKind
	label string
	id    string
}

type model struct {
	store      *store
	data       vaultData
	settings   settings
	screen     screen
	groupStack []string
	cursor     int
	formField  int
	formValues []string
	message    string
	pending    menuRow
	editing    menuRow
	width      int
	height     int
}

type backupTickMsg struct{}

func newModel(s *store, data vaultData, value settings) model {
	return model{store: s, data: data, settings: value}
}

func (m model) Init() tea.Cmd { return backupTick() }

func backupTick() tea.Cmd {
	return tea.Tick(time.Minute, func(time.Time) tea.Msg { return backupTickMsg{} })
}

func (m model) currentGroup() string {
	if len(m.groupStack) == 0 {
		return ""
	}
	return m.groupStack[len(m.groupStack)-1]
}

func (m model) rows() []menuRow {
	parent := m.currentGroup()
	var rows []menuRow
	var groups []group
	for _, value := range m.data.Groups {
		if value.ParentID == parent {
			groups = append(groups, value)
		}
	}
	for _, value := range groups {
		rows = append(rows, menuRow{kind: groupRow, label: value.Name + "  >", id: value.ID})
	}
	var sessions []session
	for _, value := range m.data.Sessions {
		if value.GroupID == parent {
			sessions = append(sessions, value)
		}
	}
	for _, value := range sessions {
		endpoint := value.Host
		if value.User != "" {
			endpoint = value.User + "@" + endpoint
		}
		protocol := value.Protocol
		if protocol == "" {
			protocol = "ssh"
		}
		rows = append(rows, menuRow{kind: sessionRow, label: value.Name + "  [" + protocol + "] " + endpoint, id: value.ID})
	}
	rows = append(rows, menuRow{kind: actionRow, label: m.tr("add_session"), id: "add-session"})
	rows = append(rows, menuRow{kind: actionRow, label: m.tr("add_group"), id: "add-group"})
	if parent == "" {
		rows = append(rows, menuRow{kind: actionRow, label: m.tr("settings"), id: "settings"})
	}
	return rows
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch value := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = value.Width, value.Height
	case backupTickMsg:
		previous := m.settings.LastBackupAt
		updated, err := m.store.backupIfDue(m.settings)
		m.settings = updated
		if err != nil {
			m.message = "Automatic backup failed: " + err.Error()
		} else if updated.LastBackupAt != "" && updated.LastBackupAt != previous {
			m.message = "Backup saved."
		}
		return m, backupTick()
	case connectedMsg:
		if value.err != nil {
			m.message = "Connection ended: " + value.err.Error()
		}
	case tea.KeyMsg:
		switch m.screen {
		case menuScreen:
			return m.updateMenu(value)
		case sessionFormScreen, groupFormScreen, settingsScreen:
			return m.updateForm(value)
		case confirmScreen:
			return m.updateConfirm(value)
		}
	}
	return m, nil
}

func (m model) updateMenu(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.rows()
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(rows)-1 {
			m.cursor++
		}
	case "shift+up", "shift+down":
		if len(rows) == 0 || (rows[m.cursor].kind != groupRow && rows[m.cursor].kind != sessionRow) {
			return m, nil
		}
		direction := -1
		if key.String() == "shift+down" {
			direction = 1
		}
		moved, err := m.moveRow(rows[m.cursor], direction)
		if err != nil {
			m.message = "Save failed: " + err.Error()
		} else if moved {
			m.cursor += direction
		}
	case "esc", "backspace":
		if len(m.groupStack) == 0 {
			return m, tea.Quit
		}
		m.groupStack = m.groupStack[:len(m.groupStack)-1]
		m.cursor = 0
	case "d", "delete":
		if len(rows) > 0 && (rows[m.cursor].kind == groupRow || rows[m.cursor].kind == sessionRow) {
			m.pending, m.screen = rows[m.cursor], confirmScreen
		}
	case "e":
		if len(rows) > 0 && (rows[m.cursor].kind == groupRow || rows[m.cursor].kind == sessionRow) {
			m.openEdit(rows[m.cursor])
		}
	case "enter":
		if len(rows) == 0 {
			return m, nil
		}
		row := rows[m.cursor]
		switch row.kind {
		case groupRow:
			m.groupStack = append(m.groupStack, row.id)
			m.cursor = 0
		case sessionRow:
			return m, m.connect(row.id)
		case actionRow:
			switch row.id {
			case "add-session":
				m.editing = menuRow{}
				m.screen, m.formField, m.formValues = sessionFormScreen, 0, []string{"", "ssh", "", "", "22", ""}
			case "add-group":
				m.editing = menuRow{}
				m.screen, m.formField, m.formValues = groupFormScreen, 0, []string{""}
			case "settings":
				m.screen, m.formField = settingsScreen, 0
				language := m.settings.Language
				if language == "" {
					language = "auto"
				}
				m.formValues = []string{m.settings.BackupDir, strconv.Itoa(m.settings.BackupHours), strconv.Itoa(m.settings.BackupMax), language}
			}
		}
	}
	return m, nil
}

func (m *model) openEdit(row menuRow) {
	m.editing, m.formField = row, 0
	if row.kind == groupRow {
		for _, value := range m.data.Groups {
			if value.ID == row.id {
				m.screen, m.formValues = groupFormScreen, []string{value.Name}
				return
			}
		}
	}
	for _, value := range m.data.Sessions {
		if value.ID != row.id {
			continue
		}
		protocol := value.Protocol
		if protocol == "" {
			protocol = "ssh"
		}
		port := value.Port
		if port == "" {
			if protocol == "telnet" {
				port = "23"
			} else {
				port = "22"
			}
		}
		m.screen, m.formValues = sessionFormScreen, []string{value.Name, protocol, value.Host, value.User, port, value.Password}
		return
	}
}

func (m *model) moveRow(row menuRow, direction int) (bool, error) {
	moved := false
	switch row.kind {
	case groupRow:
		for index, value := range m.data.Groups {
			if value.ID != row.id {
				continue
			}
			for other := index + direction; other >= 0 && other < len(m.data.Groups); other += direction {
				if m.data.Groups[other].ParentID == value.ParentID {
					m.data.Groups[index], m.data.Groups[other] = m.data.Groups[other], m.data.Groups[index]
					moved = true
				}
				break
			}
			break
		}
	case sessionRow:
		for index, value := range m.data.Sessions {
			if value.ID != row.id {
				continue
			}
			for other := index + direction; other >= 0 && other < len(m.data.Sessions); other += direction {
				if m.data.Sessions[other].GroupID == value.GroupID {
					m.data.Sessions[index], m.data.Sessions[other] = m.data.Sessions[other], m.data.Sessions[index]
					moved = true
				}
				break
			}
			break
		}
	}
	if !moved {
		return false, nil
	}
	return true, m.store.save(m.data)
}

func (m model) updateForm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c", "esc":
		m.screen, m.message, m.editing = menuScreen, "", menuRow{}
		return m, nil
	case "ctrl+b":
		if m.screen != settingsScreen {
			return m, nil
		}
		m.settings.BackupDir = strings.TrimSpace(m.formValues[0])
		updated, err := m.store.backup(m.settings)
		if err != nil {
			m.message = "Backup failed: " + err.Error()
		} else {
			m.settings, m.message = updated, "Backup saved."
		}
		return m, nil
	case "tab", "down":
		if m.formField < len(m.formValues)-1 {
			m.formField++
		}
	case "shift+tab", "up":
		if m.formField > 0 {
			m.formField--
		}
	case "left", "right":
		if m.screen == sessionFormScreen && m.formField == 1 {
			if m.formValues[1] == "ssh" {
				m.formValues[1], m.formValues[4] = "telnet", "23"
			} else {
				m.formValues[1], m.formValues[4] = "ssh", "22"
			}
		} else if m.screen == settingsScreen && m.formField == 3 {
			switch m.formValues[3] {
			case "auto":
				m.formValues[3] = "zh"
			case "zh":
				m.formValues[3] = "en"
			default:
				m.formValues[3] = "auto"
			}
		}
	case "enter":
		if m.formField < len(m.formValues)-1 {
			m.formField++
			return m, nil
		}
		return m, m.submitForm()
	case "backspace":
		value := m.formValues[m.formField]
		if len(value) > 0 {
			m.formValues[m.formField] = value[:len(value)-1]
		}
	default:
		if len(key.Runes) > 0 && key.Type == tea.KeyRunes {
			if (m.screen == sessionFormScreen && m.formField == 1) || (m.screen == settingsScreen && m.formField == 3) {
				return m, nil
			}
			m.formValues[m.formField] += string(key.Runes)
		}
	}
	return m, nil
}

func (m *model) submitForm() tea.Cmd {
	switch m.screen {
	case sessionFormScreen:
		name, host := strings.TrimSpace(m.formValues[0]), strings.TrimSpace(m.formValues[2])
		if name == "" || host == "" {
			m.message = "Name and host are required."
			return nil
		}
		updated := session{ID: newID(), GroupID: m.currentGroup(), Name: name, Protocol: m.formValues[1], Host: host, User: strings.TrimSpace(m.formValues[3]), Port: strings.TrimSpace(m.formValues[4]), Password: m.formValues[5], Created: time.Now().UTC().Format(time.RFC3339)}
		if m.editing.id == "" {
			m.data.Sessions = append(m.data.Sessions, updated)
		} else {
			for index, value := range m.data.Sessions {
				if value.ID == m.editing.id {
					updated.ID, updated.GroupID, updated.Created = value.ID, value.GroupID, value.Created
					m.data.Sessions[index] = updated
					break
				}
			}
		}
		if err := m.store.save(m.data); err != nil {
			m.message = "Save failed: " + err.Error()
			return nil
		}
		m.message, m.screen, m.cursor, m.editing = m.tr("session_saved"), menuScreen, 0, menuRow{}
	case groupFormScreen:
		name := strings.TrimSpace(m.formValues[0])
		if name == "" {
			m.message = "A group name is required."
			return nil
		}
		if m.editing.id == "" {
			m.data.Groups = append(m.data.Groups, group{ID: newID(), ParentID: m.currentGroup(), Name: name})
		} else {
			for index, value := range m.data.Groups {
				if value.ID == m.editing.id {
					m.data.Groups[index].Name = name
					break
				}
			}
		}
		if err := m.store.save(m.data); err != nil {
			m.message = "Save failed: " + err.Error()
			return nil
		}
		m.message, m.screen, m.cursor, m.editing = m.tr("group_saved"), menuScreen, 0, menuRow{}
	case settingsScreen:
		hours, err := strconv.Atoi(strings.TrimSpace(m.formValues[1]))
		if err != nil || hours < 0 {
			m.message = "Backup interval must be a non-negative number of hours."
			return nil
		}
		maximum, err := strconv.Atoi(strings.TrimSpace(m.formValues[2]))
		if err != nil || maximum < 0 {
			m.message = "Maximum backup count must be a non-negative number."
			return nil
		}
		language := m.formValues[3]
		if language != "auto" && language != "zh" && language != "en" {
			m.message = "Invalid language."
			return nil
		}
		backupDir := strings.TrimSpace(m.formValues[0])
		if backupDir != m.settings.BackupDir {
			m.settings.LastBackupAt = ""
		}
		m.settings.BackupDir, m.settings.BackupHours, m.settings.BackupMax, m.settings.Language = backupDir, hours, maximum, language
		if err := m.store.saveSettings(m.settings); err != nil {
			m.message = "Save failed: " + err.Error()
			return nil
		}
		m.message, m.screen = m.tr("settings_saved"), menuScreen
	}
	return nil
}

func (m model) connect(id string) tea.Cmd {
	for _, value := range m.data.Sessions {
		if value.ID != id {
			continue
		}
		protocol := value.Protocol
		if protocol == "" {
			protocol = "ssh"
		}
		if protocol == "telnet" {
			port := value.Port
			if port == "" {
				port = "23"
			}
			executable, err := os.Executable()
			if err != nil {
				return func() tea.Msg { return connectedMsg{err: fmt.Errorf("find ishell executable: %w", err)} }
			}
			command := exec.Command(executable, "telnet", value.Host, port, value.User)
			if value.Password == "" {
				return tea.ExecProcess(command, func(err error) tea.Msg { return connectedMsg{err: err} })
			}
			server, err := startAskpassServer(value.Password)
			if err != nil {
				return func() tea.Msg { return connectedMsg{err: fmt.Errorf("start password helper: %w", err)} }
			}
			command.Env = append(os.Environ(), askpassAddressEnv+"="+server.listener.Addr().String(), askpassTokenEnv+"="+server.token)
			return tea.ExecProcess(command, func(err error) tea.Msg { server.Close(); return connectedMsg{err: err} })
		}
		args := []string{"-o", "StrictHostKeyChecking=accept-new", "-o", "ServerAliveInterval=60", "-o", "ServerAliveCountMax=3"}
		if value.Port != "" && value.Port != "22" {
			args = append(args, "-p", value.Port)
		}
		target := value.Host
		if value.User != "" {
			target = value.User + "@" + target
		}
		args = append(args, target)
		command := exec.Command("ssh", args...)
		if value.Password == "" {
			return tea.ExecProcess(command, func(err error) tea.Msg { return connectedMsg{err: err} })
		}
		server, err := startAskpassServer(value.Password)
		if err != nil {
			return func() tea.Msg { return connectedMsg{err: fmt.Errorf("start password helper: %w", err)} }
		}
		executable, err := os.Executable()
		if err != nil {
			server.Close()
			return func() tea.Msg { return connectedMsg{err: fmt.Errorf("find ishell executable: %w", err)} }
		}
		command.Env = append(os.Environ(),
			"SSH_ASKPASS="+executable,
			"SSH_ASKPASS_REQUIRE=force",
			"DISPLAY=ishell:0",
			askpassAddressEnv+"="+server.listener.Addr().String(),
			askpassTokenEnv+"="+server.token,
		)
		return tea.ExecProcess(command, func(err error) tea.Msg {
			server.Close()
			return connectedMsg{err: err}
		})
	}
	return nil
}

type connectedMsg struct{ err error }

func (m model) View() string {
	if m.screen != menuScreen {
		if m.screen == confirmScreen {
			return m.confirmView()
		}
		return m.formView()
	}
	var builder strings.Builder
	builder.WriteString("iShell\n")
	if len(m.groupStack) == 0 {
		builder.WriteString(m.tr("connections") + "\n\n")
	} else {
		builder.WriteString(m.tr("group") + "\n\n")
	}
	for index, row := range m.rows() {
		prefix := "  "
		if index == m.cursor {
			prefix = "> "
		}
		if row.kind == actionRow {
			builder.WriteString("\n")
		}
		builder.WriteString(prefix + row.label + "\n")
	}
	if m.message != "" {
		builder.WriteString("\n" + m.message + "\n")
	}
	builder.WriteString("\n" + m.tr("menu_help"))
	return builder.String()
}

func (m model) formView() string {
	var title string
	var labels []string
	switch m.screen {
	case sessionFormScreen:
		title, labels = m.tr("add_session_title"), []string{m.tr("name"), m.tr("protocol"), m.tr("host"), m.tr("user"), m.tr("port"), m.tr("session_password")}
		if m.editing.id != "" {
			title = m.tr("edit_session_title")
		}
	case groupFormScreen:
		title, labels = m.tr("add_group_title"), []string{m.tr("name")}
		if m.editing.id != "" {
			title = m.tr("edit_group_title")
		}
	case settingsScreen:
		title, labels = m.tr("backup_title"), []string{m.tr("backup_dir"), m.tr("backup_interval"), m.tr("backup_max"), m.tr("language")}
	}
	var builder strings.Builder
	builder.WriteString(title + "\n\n")
	maxLabelWidth := 0
	for _, label := range labels {
		if displayWidth(label) > maxLabelWidth {
			maxLabelWidth = displayWidth(label)
		}
	}
	for index, label := range labels {
		prefix := "  "
		if index == m.formField {
			prefix = "> "
		}
		value := m.formValues[index]
		if m.screen == sessionFormScreen && index == 5 {
			value = mask(value)
		}
		if m.screen == settingsScreen && index == 3 {
			value = m.tr(value)
		}
		builder.WriteString(fmt.Sprintf("%s%s: %s\n", prefix, padDisplay(label, maxLabelWidth), value))
	}
	if m.message != "" {
		builder.WriteString("\n" + m.message + "\n")
	}
	if m.screen == settingsScreen {
		builder.WriteString("\n" + m.tr("backup_help"))
	} else {
		builder.WriteString("\n" + m.tr("form_help"))
	}
	return builder.String()
}

func (m model) confirmView() string {
	return "iShell\n\n" + m.pending.label + "\n\n" + m.tr("delete_confirm")
}

func (m model) updateConfirm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "n":
		m.screen = menuScreen
	case "enter", "y":
		if m.pending.kind == groupRow && m.groupHasContent(m.pending.id) {
			m.message, m.screen = m.tr("delete_group_not_empty"), menuScreen
			return m, nil
		}
		if m.pending.kind == groupRow {
			for index, value := range m.data.Groups {
				if value.ID == m.pending.id {
					m.data.Groups = append(m.data.Groups[:index], m.data.Groups[index+1:]...)
					break
				}
			}
		} else {
			for index, value := range m.data.Sessions {
				if value.ID == m.pending.id {
					m.data.Sessions = append(m.data.Sessions[:index], m.data.Sessions[index+1:]...)
					break
				}
			}
		}
		if err := m.store.save(m.data); err != nil {
			m.message = "Save failed: " + err.Error()
		} else {
			m.message = m.tr("deleted")
		}
		m.screen, m.cursor = menuScreen, 0
	}
	return m, nil
}

func (m model) groupHasContent(id string) bool {
	for _, value := range m.data.Groups {
		if value.ParentID == id {
			return true
		}
	}
	for _, value := range m.data.Sessions {
		if value.GroupID == id {
			return true
		}
	}
	return false
}

func newID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

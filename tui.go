package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
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
	backupSettingsScreen
	languageSettingsScreen
	confirmScreen
	restoreConfirmScreen
	scriptPickerScreen
	scriptFormScreen
)

type rowKind int

const (
	groupRow rowKind = iota
	sessionRow
	actionRow
	scriptRow
)

type menuRow struct {
	kind  rowKind
	label string
	id    string
}

type model struct {
	store             *store
	data              vaultData
	settings          settings
	screen            screen
	groupStack        []string
	cursor            int
	formField         int
	formValues        []string
	message           string
	pending           menuRow
	editing           menuRow
	scriptEditingID   string
	returnToSession   bool
	sessionFormValues []string
	restorePath       string
	width             int
	height            int
}

type backupTickMsg struct{}
type remoteSyncMsg struct {
	pulled int
	err    error
}
type scriptEditedMsg struct {
	content string
	err     error
}

func newModel(s *store, data vaultData, value settings) model {
	return model{store: s, data: data, settings: value}
}

func (m model) Init() tea.Cmd { return tea.Batch(backupTick(), m.checkRemoteBackups()) }

func backupTick() tea.Cmd {
	return tea.Tick(time.Minute, func(time.Time) tea.Msg { return backupTickMsg{} })
}

func (m model) currentGroup() string {
	if len(m.groupStack) == 0 {
		return ""
	}
	return m.groupStack[len(m.groupStack)-1]
}

func (m model) groupPath() string {
	if len(m.groupStack) == 0 {
		return m.tr("connections")
	}
	names := []string{m.tr("connections")}
	for _, id := range m.groupStack {
		for _, value := range m.data.Groups {
			if value.ID == id {
				names = append(names, value.Name)
				break
			}
		}
	}
	return strings.Join(names, " / ")
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
		updated, err := m.store.backupIfDue(m.settings, m.data.WebDAV)
		m.settings = updated
		if err != nil {
			m.message = "Automatic backup failed: " + err.Error()
		} else if updated.LastBackupAt != "" && updated.LastBackupAt != previous {
			m.message = "Backup saved."
		}
		return m, backupTick()
	case remoteSyncMsg:
		if value.err != nil {
			m.message = "WebDAV sync failed: " + value.err.Error()
		} else if value.pulled > 0 {
			m.message = fmt.Sprintf("Downloaded %d newer WebDAV backup(s).", value.pulled)
		}
	case connectedMsg:
		if value.err != nil {
			m.message = "Connection ended: " + value.err.Error()
		}
	case scriptEditedMsg:
		if value.err != nil {
			m.message = "Script editor failed: " + value.err.Error()
		} else {
			m.formValues[2], m.message = value.content, ""
		}
	case tea.KeyMsg:
		switch m.screen {
		case menuScreen:
			return m.updateMenu(value)
		case sessionFormScreen, groupFormScreen, backupSettingsScreen, languageSettingsScreen:
			return m.updateForm(value)
		case settingsScreen:
			return m.updateSettings(value)
		case confirmScreen:
			return m.updateConfirm(value)
		case restoreConfirmScreen:
			return m.updateRestoreConfirm(value)
		case scriptPickerScreen:
			return m.updateScriptPicker(value)
		case scriptFormScreen:
			return m.updateScriptForm(value)
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
				m.screen, m.formField, m.formValues = sessionFormScreen, 0, []string{"", "ssh", "", "", "22", "", ""}
			case "add-group":
				m.editing = menuRow{}
				m.screen, m.formField, m.formValues = groupFormScreen, 0, []string{""}
			case "settings":
				m.screen, m.cursor = settingsScreen, 0
			}
		}
	}
	return m, nil
}

func (m model) settingsRows() []menuRow {
	return []menuRow{{kind: actionRow, label: m.tr("backup_restore"), id: "backup"}, {kind: actionRow, label: m.tr("language_settings"), id: "language"}}
}

func (m model) settingsView() string {
	var builder strings.Builder
	builder.WriteString("iShell\n\n" + m.tr("settings") + "\n\n")
	for index, row := range m.settingsRows() {
		prefix := "  "
		if index == m.cursor {
			prefix = "> "
		}
		builder.WriteString(prefix + row.label + "\n")
	}
	builder.WriteString("\n" + m.tr("settings_help"))
	return builder.String()
}

func (m model) updateSettings(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.settingsRows()
	switch key.String() {
	case "esc", "backspace", "ctrl+c":
		m.screen = menuScreen
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(rows)-1 {
			m.cursor++
		}
	case "enter":
		if rows[m.cursor].id == "backup" {
			m.screen, m.formField = backupSettingsScreen, 0
			m.formValues = []string{m.settings.BackupDir, strconv.Itoa(m.settings.BackupHours), strconv.Itoa(m.settings.BackupMax), m.data.WebDAV.URL, m.data.WebDAV.Path, m.data.WebDAV.Username, m.data.WebDAV.Password, ""}
			return m, m.checkRemoteBackups()
		}
		language := m.settings.Language
		if language == "" {
			language = "auto"
		}
		m.screen, m.formField, m.formValues = languageSettingsScreen, 0, []string{language}
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
		m.screen, m.formValues = sessionFormScreen, []string{value.Name, protocol, value.Host, value.User, port, value.Password, value.InitScriptID}
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
		if m.screen == backupSettingsScreen || m.screen == languageSettingsScreen {
			m.screen, m.message = settingsScreen, ""
			return m, nil
		}
		m.screen, m.message, m.editing = menuScreen, "", menuRow{}
		return m, nil
	case "ctrl+b":
		if m.screen != backupSettingsScreen {
			return m, nil
		}
		if err := m.saveBackupSettings(); err != nil {
			m.message = "Save failed: " + err.Error()
			return m, nil
		}
		updated, err := m.store.backup(m.settings, m.data.WebDAV)
		if err != nil {
			m.message = "Backup failed: " + err.Error()
		} else {
			m.settings, m.message = updated, "Backup saved."
		}
		return m, nil
	case "ctrl+r":
		if m.screen != backupSettingsScreen {
			return m, nil
		}
		m.restorePath = strings.TrimSpace(m.formValues[7])
		if m.restorePath == "" {
			m.message = "Enter a backup directory or vault.json path first."
			return m, nil
		}
		m.screen = restoreConfirmScreen
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
		} else if m.screen == languageSettingsScreen && m.formField == 0 {
			switch m.formValues[0] {
			case "auto":
				m.formValues[0] = "zh"
			case "zh":
				m.formValues[0] = "en"
			default:
				m.formValues[0] = "auto"
			}
		}
	case "enter":
		if m.screen == sessionFormScreen && m.formField == 6 {
			m.sessionFormValues = append([]string(nil), m.formValues...)
			m.screen, m.cursor = scriptPickerScreen, 0
			return m, nil
		}
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
			if (m.screen == sessionFormScreen && (m.formField == 1 || m.formField == 6)) || (m.screen == languageSettingsScreen && m.formField == 0) {
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
		parentID := m.currentGroup()
		if m.editing.id != "" {
			for _, value := range m.data.Sessions {
				if value.ID == m.editing.id {
					parentID = value.GroupID
					break
				}
			}
		}
		if m.nameInUse(parentID, name, m.editing.id) {
			m.message = m.tr("name_in_use")
			return nil
		}
		updated := session{ID: newID(), GroupID: parentID, Name: name, Protocol: m.formValues[1], Host: host, User: strings.TrimSpace(m.formValues[3]), Port: strings.TrimSpace(m.formValues[4]), Password: m.formValues[5], InitScriptID: m.formValues[6], Created: time.Now().UTC().Format(time.RFC3339)}
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
		parentID := m.currentGroup()
		if m.editing.id != "" {
			for _, value := range m.data.Groups {
				if value.ID == m.editing.id {
					parentID = value.ParentID
					break
				}
			}
		}
		if m.nameInUse(parentID, name, m.editing.id) {
			m.message = m.tr("name_in_use")
			return nil
		}
		if m.editing.id == "" {
			m.data.Groups = append(m.data.Groups, group{ID: newID(), ParentID: parentID, Name: name})
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
	case backupSettingsScreen:
		if err := m.saveBackupSettings(); err != nil {
			m.message = "Save failed: " + err.Error()
			return nil
		}
		m.message, m.screen = m.tr("settings_saved"), settingsScreen
	case languageSettingsScreen:
		language := m.formValues[0]
		if language != "auto" && language != "zh" && language != "en" {
			m.message = "Invalid language."
			return nil
		}
		m.settings.Language = language
		if err := m.store.saveSettings(m.settings); err != nil {
			m.message = "Save failed: " + err.Error()
			return nil
		}
		m.message, m.screen = m.tr("settings_saved"), settingsScreen
	}
	return nil
}

func (m *model) saveBackupSettings() error {
	hours, err := strconv.Atoi(strings.TrimSpace(m.formValues[1]))
	if err != nil || hours < 0 {
		return errors.New("backup interval must be a non-negative number of hours")
	}
	maximum, err := strconv.Atoi(strings.TrimSpace(m.formValues[2]))
	if err != nil || maximum < 0 {
		return errors.New("maximum backup count must be a non-negative number")
	}
	backupDir := strings.TrimSpace(m.formValues[0])
	if backupDir != m.settings.BackupDir {
		m.settings.LastBackupAt = ""
	}
	m.settings.BackupDir, m.settings.BackupHours, m.settings.BackupMax = backupDir, hours, maximum
	m.data.WebDAV = webDAVConfig{URL: strings.TrimSpace(m.formValues[3]), Path: strings.TrimSpace(m.formValues[4]), Username: strings.TrimSpace(m.formValues[5]), Password: m.formValues[6]}
	if err := m.store.save(m.data); err != nil {
		return err
	}
	return m.store.saveSettings(m.settings)
}

func (m model) nameInUse(parentID, name, ignoreID string) bool {
	for _, value := range m.data.Groups {
		if value.ParentID == parentID && value.ID != ignoreID && strings.EqualFold(value.Name, name) {
			return true
		}
	}
	for _, value := range m.data.Sessions {
		if value.GroupID == parentID && value.ID != ignoreID && strings.EqualFold(value.Name, name) {
			return true
		}
	}
	return false
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
			script, hasScript := m.initScript(value.InitScriptID)
			if value.Password == "" && !hasScript {
				return tea.ExecProcess(command, func(err error) tea.Msg { return connectedMsg{err: err} })
			}
			server, err := startAskpassServer(sessionSecrets{Password: value.Password, Script: script.Content})
			if err != nil {
				return func() tea.Msg { return connectedMsg{err: fmt.Errorf("start password helper: %w", err)} }
			}
			command.Env = append(os.Environ(), askpassAddressEnv+"="+server.listener.Addr().String(), askpassTokenEnv+"="+server.token)
			return tea.ExecProcess(command, func(err error) tea.Msg { server.Close(); return connectedMsg{err: err} })
		}
		args := []string{"-o", "StrictHostKeyChecking=accept-new", "-o", "ServerAliveInterval=60", "-o", "ServerAliveCountMax=3"}
		script, hasScript := m.initScript(value.InitScriptID)
		if hasScript {
			args = append(args, "-tt")
		}
		if value.Port != "" && value.Port != "22" {
			args = append(args, "-p", value.Port)
		}
		target := value.Host
		if value.User != "" {
			target = value.User + "@" + target
		}
		args = append(args, target)
		if hasScript {
			args = append(args, remoteInitCommand(script))
		}
		command := exec.Command("ssh", args...)
		if value.Password == "" {
			return tea.ExecProcess(command, func(err error) tea.Msg { return connectedMsg{err: err} })
		}
		server, err := startAskpassServer(sessionSecrets{Password: value.Password})
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

func (m model) initScript(id string) (initScript, bool) {
	if id == "" {
		return initScript{}, false
	}
	for _, value := range m.data.Scripts {
		if value.ID == id {
			return value, true
		}
	}
	return initScript{}, false
}

func remoteInitCommand(script initScript) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(script.Content))
	interpreter := script.Interpreter
	if interpreter != "bash" {
		interpreter = "sh"
	}
	body := "eval \"$(printf %s '" + encoded + "' | (base64 -d 2>/dev/null || base64 -D))\"; exec " + interpreter + " -i"
	return interpreter + " -lc " + posixQuote(body)
}

func posixQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type connectedMsg struct{ err error }

func (m model) View() string {
	if m.screen != menuScreen {
		if m.screen == confirmScreen {
			return m.confirmView()
		}
		if m.screen == restoreConfirmScreen {
			return m.restoreConfirmView()
		}
		if m.screen == scriptPickerScreen {
			return m.scriptPickerView()
		}
		if m.screen == settingsScreen {
			return m.settingsView()
		}
		return m.formView()
	}
	var builder strings.Builder
	builder.WriteString("iShell\n")
	builder.WriteString(m.groupPath() + "\n\n")
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
		title, labels = m.tr("add_session_title"), []string{m.tr("name"), m.tr("protocol"), m.tr("host"), m.tr("user"), m.tr("port"), m.tr("session_password"), m.tr("init_script")}
		if m.editing.id != "" {
			title = m.tr("edit_session_title")
		}
	case groupFormScreen:
		title, labels = m.tr("add_group_title"), []string{m.tr("name")}
		if m.editing.id != "" {
			title = m.tr("edit_group_title")
		}
	case backupSettingsScreen:
		title, labels = m.tr("backup_restore"), []string{m.tr("backup_dir"), m.tr("backup_interval"), m.tr("backup_max"), m.tr("webdav_url"), m.tr("webdav_path"), m.tr("webdav_user"), m.tr("webdav_password"), m.tr("restore_source")}
	case languageSettingsScreen:
		title, labels = m.tr("language_settings"), []string{m.tr("language")}
	case scriptFormScreen:
		title, labels = m.tr("new_script_title"), []string{m.tr("script_name"), m.tr("interpreter"), m.tr("script_content"), m.tr("save")}
		if m.scriptEditingID != "" {
			title = m.tr("edit_script_title")
		}
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
		if m.screen == sessionFormScreen && index == 6 {
			value = m.scriptName(value)
		}
		if m.screen == languageSettingsScreen && index == 0 {
			value = m.tr(value)
		}
		if m.screen == backupSettingsScreen && index == 6 {
			value = mask(value)
		}
		if m.screen == scriptFormScreen && index == 2 {
			value = m.tr("edit_content") + fmt.Sprintf(" (%d bytes)", len(value))
		}
		builder.WriteString(fmt.Sprintf("%s%s: %s\n", prefix, padDisplay(label, maxLabelWidth), value))
	}
	if m.message != "" {
		builder.WriteString("\n" + m.message + "\n")
	}
	if m.screen == backupSettingsScreen {
		builder.WriteString("\n" + m.tr("backup_help"))
	} else if m.screen == scriptFormScreen {
		builder.WriteString("\n" + m.tr("script_form_help"))
	} else {
		builder.WriteString("\n" + m.tr("form_help"))
	}
	return builder.String()
}

func (m model) scriptName(id string) string {
	if id == "" {
		return m.tr("none")
	}
	for _, value := range m.data.Scripts {
		if value.ID == id {
			return value.Name + " [" + value.Interpreter + "]"
		}
	}
	return m.tr("missing_script")
}

func (m model) scriptPickerRows() []menuRow {
	rows := []menuRow{{kind: actionRow, label: m.tr("none"), id: ""}, {kind: actionRow, label: m.tr("new_script"), id: "new-script"}}
	for _, value := range m.data.Scripts {
		rows = append(rows, menuRow{kind: scriptRow, label: value.Name + " [" + value.Interpreter + "]", id: value.ID})
	}
	return rows
}

func (m model) scriptPickerView() string {
	var builder strings.Builder
	builder.WriteString("iShell\n\n" + m.tr("select_script") + "\n\n")
	for index, row := range m.scriptPickerRows() {
		prefix := "  "
		if index == m.cursor {
			prefix = "> "
		}
		builder.WriteString(prefix + row.label + "\n")
	}
	builder.WriteString("\n" + m.tr("script_picker_help"))
	return builder.String()
}

func (m model) updateScriptPicker(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.scriptPickerRows()
	switch key.String() {
	case "esc", "backspace":
		m.formValues, m.screen, m.cursor = m.sessionFormValues, sessionFormScreen, 0
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(rows)-1 {
			m.cursor++
		}
	case "e":
		if len(rows) > 0 && rows[m.cursor].kind == scriptRow {
			m.openScriptForm(rows[m.cursor].id, false)
		}
	case "enter":
		if len(rows) == 0 {
			return m, nil
		}
		row := rows[m.cursor]
		if row.id == "new-script" {
			m.openScriptForm("", true)
		} else if row.kind == scriptRow {
			m.formValues, m.screen, m.cursor = m.sessionFormValues, sessionFormScreen, 0
			m.formValues[6] = row.id
		} else {
			m.formValues, m.screen, m.cursor = m.sessionFormValues, sessionFormScreen, 0
			m.formValues[6] = ""
		}
	}
	return m, nil
}

func (m *model) openScriptForm(id string, returnToSession bool) {
	m.scriptEditingID, m.returnToSession, m.formField = id, returnToSession, 0
	if id == "" {
		m.screen, m.formValues = scriptFormScreen, []string{"", "sh", "", m.tr("save")}
		return
	}
	for _, value := range m.data.Scripts {
		if value.ID == id {
			m.screen, m.formValues = scriptFormScreen, []string{value.Name, value.Interpreter, value.Content, m.tr("save")}
			return
		}
	}
}

func (m model) updateScriptForm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "ctrl+c":
		if m.returnToSession {
			m.formValues, m.screen = m.sessionFormValues, sessionFormScreen
		} else {
			m.screen = scriptPickerScreen
		}
	case "tab", "down":
		if m.formField < len(m.formValues)-1 {
			m.formField++
		}
	case "shift+tab", "up":
		if m.formField > 0 {
			m.formField--
		}
	case "left", "right":
		if m.formField == 1 {
			if m.formValues[1] == "sh" {
				m.formValues[1] = "bash"
			} else {
				m.formValues[1] = "sh"
			}
		}
	case "enter":
		switch m.formField {
		case 0, 1:
			m.formField++
		case 2:
			return m, editScriptContent(m.formValues[2])
		case 3:
			return m, m.submitScriptForm()
		}
	case "backspace":
		if m.formField < 2 {
			value := m.formValues[m.formField]
			if len(value) > 0 {
				m.formValues[m.formField] = value[:len(value)-1]
			}
		}
	default:
		if len(key.Runes) > 0 && key.Type == tea.KeyRunes && m.formField == 0 {
			m.formValues[0] += string(key.Runes)
		}
	}
	return m, nil
}

func (m *model) submitScriptForm() tea.Cmd {
	name := strings.TrimSpace(m.formValues[0])
	if name == "" {
		m.message = "A script name is required."
		return nil
	}
	interpreter := m.formValues[1]
	if interpreter != "sh" && interpreter != "bash" {
		m.message = "Choose sh or bash."
		return nil
	}
	updated := initScript{ID: newID(), Name: name, Interpreter: interpreter, Content: m.formValues[2]}
	if m.scriptEditingID == "" {
		m.data.Scripts = append(m.data.Scripts, updated)
	} else {
		for index, value := range m.data.Scripts {
			if value.ID == m.scriptEditingID {
				updated.ID = value.ID
				m.data.Scripts[index] = updated
				break
			}
		}
	}
	if err := m.store.save(m.data); err != nil {
		m.message = "Save failed: " + err.Error()
		return nil
	}
	if m.returnToSession {
		m.formValues, m.screen, m.cursor = m.sessionFormValues, sessionFormScreen, 0
		m.formValues[6] = updated.ID
	} else {
		m.screen, m.cursor = scriptPickerScreen, 0
	}
	m.scriptEditingID, m.returnToSession, m.message = "", false, m.tr("script_saved")
	return nil
}

func editScriptContent(content string) tea.Cmd {
	file, err := os.CreateTemp("", "ishell-script-*")
	if err != nil {
		return func() tea.Msg { return scriptEditedMsg{err: err} }
	}
	path := file.Name()
	if err := file.Chmod(0600); err != nil {
		file.Close()
		os.Remove(path)
		return func() tea.Msg { return scriptEditedMsg{err: err} }
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		os.Remove(path)
		return func() tea.Msg { return scriptEditedMsg{err: err} }
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return func() tea.Msg { return scriptEditedMsg{err: err} }
	}
	command := editorCommand(path)
	return tea.ExecProcess(command, func(runErr error) tea.Msg {
		edited, readErr := os.ReadFile(path)
		removeErr := os.Remove(path)
		if runErr != nil {
			return scriptEditedMsg{err: runErr}
		}
		if readErr != nil {
			return scriptEditedMsg{err: readErr}
		}
		if removeErr != nil {
			return scriptEditedMsg{err: removeErr}
		}
		return scriptEditedMsg{content: string(edited)}
	})
}

func editorCommand(path string) *exec.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		if runtime.GOOS == "windows" {
			editor = "notepad.exe"
		} else {
			editor = "vi"
		}
	}
	parts := strings.Fields(editor)
	return exec.Command(parts[0], append(parts[1:], path)...)
}

func (m model) confirmView() string {
	return "iShell\n\n" + m.pending.label + "\n\n" + m.tr("delete_confirm")
}

func (m model) restoreConfirmView() string {
	return "iShell\n\n" + m.tr("restore_confirm") + "\n" + m.restorePath + "\n\n" + m.tr("restore_help")
}

func (m model) updateRestoreConfirm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "n":
		m.screen = backupSettingsScreen
	case "enter", "y":
		data, err := m.store.restore(m.restorePath)
		if err != nil {
			m.message, m.screen = "Restore failed: "+err.Error(), backupSettingsScreen
			return m, nil
		}
		m.data, m.screen, m.cursor, m.message = data, menuScreen, 0, m.tr("restored")
	}
	return m, nil
}

func (m model) checkRemoteBackups() tea.Cmd {
	config, localDir, maximum := m.data.WebDAV, m.settings.BackupDir, m.settings.BackupMax
	if !config.configured() || strings.TrimSpace(localDir) == "" {
		return nil
	}
	return func() tea.Msg {
		pulled, err := pullNewWebDAVBackups(config, localDir, maximum)
		return remoteSyncMsg{pulled: pulled, err: err}
	}
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

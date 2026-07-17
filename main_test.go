package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestVaultEncryptionRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	salt := bytes.Repeat([]byte{2}, 16)
	file, err := encrypt(key, salt, true, []byte(`{"groups":[{"name":"production"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if file.Ciphertext == `{"groups":[{"name":"production"}]}` {
		t.Fatal("vault was not encrypted")
	}
	plain, err := decrypt(key, file)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != `{"groups":[{"name":"production"}]}` {
		t.Fatalf("unexpected plaintext: %s", plain)
	}
	if _, err := decrypt(bytes.Repeat([]byte{3}, 32), file); err == nil {
		t.Fatal("wrong key decrypted vault")
	}
}

func TestDeleteLastRunePreservesUTF8(t *testing.T) {
	value := "Command 名称中"
	for _, want := range []string{"Command 名称", "Command 名", "Command ", "Command"} {
		value = deleteLastRune(value)
		if value != want {
			t.Fatalf("deleteLastRune = %q, want %q", value, want)
		}
	}
}

func TestPruneBackupsKeepsMostRecentDirectories(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"ishell-20260101-000000", "ishell-20260102-000000", "ishell-20260103-000000", "not-ishell", "ishell-not-a-date"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneBackups(root, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "ishell-20260101-000000")); !os.IsNotExist(err) {
		t.Fatal("old backup was not removed")
	}
	for _, name := range []string{"ishell-20260102-000000", "ishell-20260103-000000", "not-ishell", "ishell-not-a-date"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("%s should remain: %v", name, err)
		}
	}
}

func TestReadSettingsNormalizesNullBackupDirectory(t *testing.T) {
	dir := t.TempDir()
	s := &store{settingsPath: filepath.Join(dir, "settings.json")}
	if err := os.WriteFile(s.settingsPath, []byte(`{"backup_dir":"\u0000\u0000","backup_hours":0,"backup_max":0}`), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := s.readSettings()
	if err != nil || value.BackupDir != "" {
		t.Fatalf("normalized settings = %#v, %v", value, err)
	}
	contents, err := os.ReadFile(s.settingsPath)
	if err != nil || strings.Contains(string(contents), `\u0000`) {
		t.Fatalf("normalized settings were not persisted: %q, %v", contents, err)
	}
}

func TestBackupSettingsNormalizeNullCharacters(t *testing.T) {
	dir := t.TempDir()
	m := newModel(&store{settingsPath: filepath.Join(dir, "settings.json")}, vaultData{}, settings{})
	if err := m.saveBackupSettingsValues([]string{"\x00", "\x000", "\x000", "", ""}); err != nil {
		t.Fatal(err)
	}
	if m.settings.BackupDir != "" || m.settings.BackupHours != 0 || m.settings.BackupMax != 0 {
		t.Fatalf("normalized backup settings = %#v", m.settings)
	}
}

func TestTelnetLoginStateFillsUserAndPasswordOnce(t *testing.T) {
	state := telnetLoginState{user: "admin", password: "secret"}
	if reply := state.observe("login:"); len(reply) != 1 || reply[0] != "admin\r\n" {
		t.Fatalf("unexpected login reply: %#v", reply)
	}
	if reply := state.observe("Password:"); len(reply) != 1 || reply[0] != "secret\r\n" {
		t.Fatalf("unexpected password reply: %#v", reply)
	}
	if reply := state.observe("Password:"); reply != nil {
		t.Fatalf("password was sent twice: %#v", reply)
	}
}

func TestTelnetLoginStateSendsInitScriptAfterPassword(t *testing.T) {
	state := telnetLoginState{user: "admin", password: "secret", script: "cd /srv\nprintf ready"}
	state.observe("login:")
	replies := state.observe("password:")
	if len(replies) != 2 || replies[0] != "secret\r\n" || replies[1] != "cd /srv\r\nprintf ready\r\n" {
		t.Fatalf("unexpected Telnet initialization: %#v", replies)
	}
}

func TestRemoteInitCommandKeepsScriptOutOfShellSyntax(t *testing.T) {
	command := remoteInitCommand(initScript{Interpreter: "bash", Content: "cd '/srv/app'\necho $HOME"})
	if !strings.HasPrefix(command, "bash -lc '") || !strings.Contains(command, "Y2QgJy9zcnYvYXBwJwplY2hvICRIT01F") || !strings.Contains(command, "exec bash -i") {
		t.Fatalf("unexpected remote command: %q", command)
	}
}

func TestRowsKeepSavedManualOrder(t *testing.T) {
	model := newModel(nil, vaultData{
		Groups:   []group{{ID: "second", Name: "Second"}, {ID: "first", Name: "First"}},
		Sessions: []session{{ID: "b", Name: "B"}, {ID: "a", Name: "A"}},
	}, settings{})
	rows := model.rows()
	got := []string{rows[0].id, rows[1].id, rows[2].id, rows[3].id}
	want := []string{"second", "first", "b", "a"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("manual order changed: got %v, want %v", got, want)
		}
	}
}

func TestNameInUseCoversGroupsAndSessions(t *testing.T) {
	model := newModel(nil, vaultData{Groups: []group{{ID: "group", Name: "Shared"}}, Sessions: []session{{ID: "session", Name: "Connection"}}}, settings{})
	if !model.nameInUse("", "shared", "") || !model.nameInUse("", "CONNECTION", "") {
		t.Fatal("same-level names should be case-insensitively unique")
	}
	if model.nameInUse("", "Shared", "group") || model.nameInUse("", "Connection", "session") {
		t.Fatal("editing an item should ignore its own name")
	}
}

func TestQuickCommandModeKeepsSavedOrderAndSwitchesAtRoot(t *testing.T) {
	m := newModel(nil, vaultData{
		CommandGroups: []commandGroup{{ID: "second", Name: "Second"}, {ID: "first", Name: "First"}},
		Commands:      []quickCommand{{ID: "b", Name: "B", Command: "echo b", Platform: runtime.GOOS}, {ID: "a", Name: "A", Command: "echo a", Platform: runtime.GOOS}},
	}, settings{})
	m.mode = commandMode
	rows := m.rows()
	got := []string{rows[0].id, rows[1].id, rows[2].id, rows[3].id}
	want := []string{"second", "first", "b", "a"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("quick command order changed: got %v, want %v", got, want)
		}
	}

	updated, _ := m.updateMenu(tea.KeyMsg{Type: tea.KeyLeft})
	if updated.(model).mode != shellMode {
		t.Fatal("left arrow should select Shell at the root")
	}
	updated, _ = updated.(model).updateMenu(tea.KeyMsg{Type: tea.KeyRight})
	if updated.(model).mode != commandMode {
		t.Fatal("right arrow should select Quick Commands at the root")
	}
}

func TestMigrateLegacyCommandPlatforms(t *testing.T) {
	data := vaultData{Commands: []quickCommand{{ID: "legacy", Command: "echo legacy"}, {ID: "tagged", Command: "echo tagged", Platform: "linux"}}}
	if !migrateLegacyCommandPlatforms(&data) {
		t.Fatal("legacy command should be migrated")
	}
	if data.Commands[0].Platform != runtime.GOOS || data.Commands[1].Platform != "linux" {
		t.Fatalf("unexpected platform migration: %#v", data.Commands)
	}
	if migrateLegacyCommandPlatforms(&data) {
		t.Fatal("migrated commands should not be changed again")
	}
}

func TestInstalledExecutablePathUsesUserLocalBin(t *testing.T) {
	if runtime.GOOS != "windows" {
		return
	}
	path, err := installedExecutablePath()
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := filepath.Join(".ishell", "bin", "ishell.exe")
	if !strings.HasSuffix(path, wantSuffix) {
		t.Fatalf("installed executable path = %q, want suffix %q", path, wantSuffix)
	}
}

func TestMenuHeaderShowsVersionAndAvoidsDuplicateRootTitle(t *testing.T) {
	m := newModel(nil, vaultData{}, settings{Language: "en"})
	m.mode = commandMode
	view := m.View()
	if !strings.Contains(view, "iShell v"+appVersion) || !strings.Contains(view, "[Quick Commands]") {
		t.Fatalf("unexpected menu header: %q", view)
	}
	if strings.Contains(view, "[Quick Commands]\nQuick Commands") {
		t.Fatalf("root title should not duplicate the active tab: %q", view)
	}
}

func TestQuickCommandHeaderHasChineseTranslations(t *testing.T) {
	if got, want := translate("zh", "shell"), "终端会话"; got != want {
		t.Fatalf("shell translation = %q, want %q", got, want)
	}
	if got, want := translate("zh", "quick_commands"), "快捷命令"; got != want {
		t.Fatalf("quick command translation = %q, want %q", got, want)
	}
}

func TestVersionComparison(t *testing.T) {
	for _, test := range []struct {
		candidate string
		current   string
		want      bool
	}{
		{"v1.0.1", "1.0.0", true},
		{"v1.1.0", "1.0.9", true},
		{"v2.0.0", "1.9.9", true},
		{"v1.0.0", "1.0.0", false},
		{"v0.9.9", "1.0.0", false},
		{"latest", "1.0.0", false},
	} {
		if got := isVersionNewer(test.candidate, test.current); got != test.want {
			t.Fatalf("isVersionNewer(%q, %q) = %t, want %t", test.candidate, test.current, got, test.want)
		}
	}
}

func TestReleaseAssetMatching(t *testing.T) {
	if !releaseAssetMatches("ishell_1.0.1_Windows_x86_64.zip", "v1.0.1", "windows", "amd64") {
		t.Fatal("Windows AMD64 release asset should match")
	}
	if !releaseAssetMatches("ishell_1.0.1_Darwin_arm64.zip", "v1.0.1", "darwin", "arm64") {
		t.Fatal("macOS ARM64 release asset should match")
	}
	if releaseAssetMatches("ishell_1.0.1_Linux_x86_64.zip", "v1.0.1", "windows", "amd64") {
		t.Fatal("different operating system should not match")
	}
}

func TestReleaseChecksumVerification(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "ishell_1.0.1_Windows_x86_64.zip")
	contents := []byte("release archive")
	if err := os.WriteFile(archive, contents, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(contents)
	checksums := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(checksums, []byte(hex.EncodeToString(hash[:])+"  "+filepath.Base(archive)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyReleaseChecksum(archive, checksums, filepath.Base(archive)); err != nil {
		t.Fatal(err)
	}
}

func TestQuickCommandFormPersistsEncryptedVaultData(t *testing.T) {
	dir := t.TempDir()
	s := &store{dir: dir, vaultPath: filepath.Join(dir, "vault.json"), key: bytes.Repeat([]byte{1}, 32), salt: bytes.Repeat([]byte{2}, 16), password: true}
	m := newModel(s, vaultData{}, settings{})
	m.mode = commandMode
	m.commandGroupStack = []string{"tools"}
	m.screen, m.formValues = commandFormScreen, []string{"Start Codex", "cd C:\\work && codex"}
	m.submitForm()

	if len(m.data.Commands) != 1 || m.data.Commands[0].GroupID != "tools" || m.data.Commands[0].Platform != runtime.GOOS || m.data.Commands[0].CreatedAt == "" {
		t.Fatalf("unexpected quick command: %#v", m.data.Commands)
	}
	contents, err := os.ReadFile(s.vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "Start Codex") || strings.Contains(string(contents), "cd C:\\work") {
		t.Fatal("quick command should be stored in the encrypted vault")
	}
	var file vaultFile
	if err := json.Unmarshal(contents, &file); err != nil {
		t.Fatal(err)
	}
	plaintext, err := decrypt(s.key, file)
	if err != nil {
		t.Fatal(err)
	}
	var restored vaultData
	if err := json.Unmarshal(plaintext, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Commands) != 1 || restored.Commands[0].Command != "cd C:\\work && codex" {
		t.Fatalf("quick command was not recovered from the vault: %#v", restored.Commands)
	}
}

func TestCrossPlatformCommandIsMarkedAndCannotRun(t *testing.T) {
	target := "windows"
	if runtime.GOOS == target {
		target = "linux"
	}
	m := newModel(nil, vaultData{Commands: []quickCommand{{ID: "foreign", Name: "Foreign", Command: "echo no", Platform: target}}}, settings{})
	m.mode = commandMode
	if got := m.rows()[0].label; !strings.Contains(got, "[x] ") {
		t.Fatalf("cross-platform command label = %q", got)
	}
	cmd := m.startCommand("foreign")
	if cmd == nil {
		t.Fatal("cross-platform command should return a mismatch message")
	}
	message, ok := cmd().(commandPlatformMismatchMsg)
	if !ok || message.target != target || message.current != runtime.GOOS {
		t.Fatalf("unexpected command message: %#v", message)
	}
}

func TestNumberedRowsStartAtOneAndSkipActions(t *testing.T) {
	m := newModel(nil, vaultData{
		Groups:   []group{{ID: "group", Name: "Group"}},
		Sessions: []session{{ID: "session", Name: "Session"}},
	}, settings{})
	rows := m.rows()
	if !strings.HasPrefix(rows[0].label, "[1] ") || !strings.HasPrefix(rows[1].label, "[2] ") || strings.HasPrefix(rows[2].label, "[") {
		t.Fatalf("unexpected numbered rows: %#v", rows)
	}
}

func TestNumericShortcutsActivateShellAndCommandGroups(t *testing.T) {
	m := newModel(nil, vaultData{
		Groups:   []group{{ID: "group", Name: "Group"}},
		Sessions: []session{{ID: "session", Name: "Session", Host: "example.test"}},
	}, settings{})
	updated, _ := m.updateMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if got := updated.(model).groupStack; !slices.Equal(got, []string{"group"}) {
		t.Fatalf("shortcut 1 group stack = %#v", got)
	}
	if _, command := m.updateMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}); command == nil {
		t.Fatal("shortcut 2 should trigger the session")
	}

	m = newModel(nil, vaultData{
		CommandGroups: []commandGroup{{ID: "group", Name: "Group"}},
		Commands:      []quickCommand{{ID: "command", Name: "Command", Command: "echo ok", Platform: runtime.GOOS}},
	}, settings{})
	m.mode = commandMode
	updated, _ = m.updateMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if got := updated.(model).commandGroupStack; !slices.Equal(got, []string{"group"}) {
		t.Fatalf("shortcut 1 command group stack = %#v", got)
	}
	if _, command := m.updateMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}); command == nil {
		t.Fatal("shortcut 2 should trigger the quick command")
	}

	rows := make([]menuRow, 10)
	for index := range rows {
		rows[index] = menuRow{kind: sessionRow, id: strconv.Itoa(index + 1)}
	}
	if row, found := numberedRow(rows, "0"); !found || row.id != "10" {
		t.Fatalf("shortcut 0 should select item 10: %#v, %t", row, found)
	}
}

func TestQuickCommandProcessUsesPowerShellAliasesAndKeepsWindowsShellOpen(t *testing.T) {
	command := quickCommandProcess("ls")
	if runtime.GOOS == "windows" {
		if !slices.Contains(command.Args, "-NoExit") || !slices.Contains(command.Args, "ls") {
			t.Fatalf("PowerShell command args = %#v", command.Args)
		}
		command = quickCommandProcess("cd C:\\work && codex")
		if got, want := command.Args, []string{"cmd.exe", "/D", "/S", "/K", "cd C:\\work && codex"}; !slices.Equal(got, want) {
			t.Fatalf("Windows command args = %#v, want %#v", got, want)
		}
		return
	}
	if len(command.Args) != 3 || command.Args[1] != "-lc" || command.Args[2] != "ls" {
		t.Fatalf("unexpected Unix command args: %#v", command.Args)
	}
}

func TestCommandPlaceholdersPromptOnceAndAllowEmptyValues(t *testing.T) {
	template := "shutdown -s -t {{时间}} && echo {{地点}} {{ 时间 }}"
	if got, want := commandPlaceholderNames(template), []string{"时间", "地点"}; !slices.Equal(got, want) {
		t.Fatalf("placeholder names = %#v, want %#v", got, want)
	}
	if got, want := applyCommandParameters(template, []string{"时间", "地点"}, []string{"", "Shanghai"}), "shutdown -s -t  && echo Shanghai "; got != want {
		t.Fatalf("command parameters = %q, want %q", got, want)
	}
}

func TestCommandWithPlaceholdersOpensParameterForm(t *testing.T) {
	m := newModel(nil, vaultData{Commands: []quickCommand{{ID: "shutdown", Name: "Schedule shutdown", Command: "shutdown -s -t {{seconds}}", Platform: runtime.GOOS}}}, settings{})
	if cmd := m.startCommand("shutdown"); cmd != nil {
		t.Fatal("command with placeholders should prompt before executing")
	}
	if m.screen != commandArgumentsScreen || !slices.Equal(m.commandParameters, []string{"seconds"}) || len(m.formValues) != 1 {
		t.Fatalf("unexpected parameter form state: %#v", m)
	}
}

func TestGroupPathIncludesEveryOpenGroup(t *testing.T) {
	model := newModel(nil, vaultData{Groups: []group{
		{ID: "production", Name: "Production"},
		{ID: "database", ParentID: "production", Name: "Database"},
	}}, settings{Language: "en"})
	model.groupStack = []string{"production", "database"}
	if got, want := model.groupPath(), "Connections / Production / Database"; got != want {
		t.Fatalf("group path = %q, want %q", got, want)
	}
}

func TestAskpassServerRequiresToken(t *testing.T) {
	server, err := startAskpassServer(sessionSecrets{Password: "session-secret", Script: "echo ready"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	connection, err := net.Dial("tcp", server.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(connection, server.token); err != nil {
		t.Fatal(err)
	}
	contents, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var secrets sessionSecrets
	if err := json.Unmarshal([]byte(contents), &secrets); err != nil {
		t.Fatal(err)
	}
	if secrets.Password != "session-secret" || secrets.Script != "echo ready" {
		t.Fatalf("unexpected secrets: %#v", secrets)
	}
	connection.Close()
}

func TestWebDAVUploadAndPull(t *testing.T) {
	backupName := "ishell-20260717-120000"
	var uploaded []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		user, password, ok := request.BasicAuth()
		if !ok || user != "alice" || password != "secret" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch request.Method {
		case "MKCOL":
			writer.WriteHeader(http.StatusCreated)
		case "PUT":
			uploaded, _ = io.ReadAll(request.Body)
			writer.WriteHeader(http.StatusCreated)
		case "PROPFIND":
			writer.Header().Set("Content-Type", "application/xml")
			writer.WriteHeader(207)
			fmt.Fprintf(writer, `<multistatus xmlns="DAV:"><response><href>/backups/%s/</href></response></multistatus>`, backupName)
		case "GET":
			writer.WriteHeader(http.StatusOK)
			writer.Write(uploaded)
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	config := webDAVConfig{URL: server.URL, Path: "backups", Username: "alice", Password: "secret"}
	if err := uploadWebDAVBackup(config, backupName, []byte("encrypted-vault"), 0); err != nil {
		t.Fatal(err)
	}
	if string(uploaded) != "encrypted-vault" {
		t.Fatalf("unexpected upload: %q", uploaded)
	}
	local := t.TempDir()
	pulled, err := pullNewWebDAVBackups(config, local, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pulled != 1 {
		t.Fatalf("pulled %d backups", pulled)
	}
	contents, err := os.ReadFile(filepath.Join(local, backupName, "vault.json"))
	if err != nil || string(contents) != "encrypted-vault" {
		t.Fatalf("unexpected pulled vault: %q, %v", contents, err)
	}
}

func TestWebDAVArchiveRoundTripAndPermissionTest(t *testing.T) {
	archiveContents, err := archiveVault([]byte("encrypted-vault"))
	if err != nil {
		t.Fatal(err)
	}
	if vault, err := extractVaultArchive(archiveContents); err != nil || string(vault) != "encrypted-vault" {
		t.Fatalf("archive extraction = %q, %v", vault, err)
	}
	name := "backup-user-20260717230000.zip"
	var stored []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case "MKCOL":
			writer.WriteHeader(http.StatusCreated)
		case "PUT":
			stored, _ = io.ReadAll(request.Body)
			writer.WriteHeader(http.StatusCreated)
		case "DELETE":
			writer.WriteHeader(http.StatusNoContent)
		case "PROPFIND":
			writer.Header().Set("Content-Type", "application/xml")
			writer.WriteHeader(207)
			fmt.Fprintf(writer, `<multistatus xmlns="DAV:"><response><href>/backups/%s</href></response></multistatus>`, name)
		case "GET":
			writer.WriteHeader(http.StatusOK)
			writer.Write(stored)
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	config := webDAVConfig{URL: server.URL, Path: "backups"}
	if err := testWebDAV(config); err != nil {
		t.Fatal(err)
	}
	if err := uploadWebDAVArchive(config, name, archiveContents, 0); err != nil {
		t.Fatal(err)
	}
	archives, err := listWebDAVArchives(config)
	if err != nil || len(archives) != 1 || archives[0].Name != name {
		t.Fatalf("archives = %#v, %v", archives, err)
	}
	downloaded, err := downloadWebDAVArchive(config, name)
	if err != nil || !bytes.Equal(downloaded, archiveContents) {
		t.Fatalf("downloaded archive = %d bytes, %v", len(downloaded), err)
	}
}

func TestMissingWebDAVBackupCollectionIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "PROPFIND" {
			t.Fatalf("unexpected request method: %s", request.Method)
		}
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	config := webDAVConfig{URL: server.URL, Path: "ishell"}
	if backups, err := listWebDAVBackups(config); err != nil || len(backups) != 0 {
		t.Fatalf("legacy backups = %#v, %v", backups, err)
	}
	if archives, err := listWebDAVArchives(config); err != nil || len(archives) != 0 {
		t.Fatalf("archives = %#v, %v", archives, err)
	}
}

func TestWebDAVArchiveNameUsesLocalHostAndBackupLabel(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	name := webDAVArchiveName(webDAVConfig{URL: "https://git.perfy576.com:8444", Username: "perfy576"}, time.Date(2026, 7, 17, 23, 50, 54, 0, time.UTC), "manual")
	want := safeArchiveNamePart(host) + "-perfy576-20260717235054-manual.zip"
	if name != want {
		t.Fatalf("archive name = %q, want %q", name, want)
	}
	if _, err := validateBackupLabel("project release"); err != nil {
		t.Fatal(err)
	}
	if label, err := validateBackupLabel(""); err != nil || label != "manual" {
		t.Fatalf("empty manual label = %q, %v", label, err)
	}
	for _, invalid := range []string{"bad/name", "bad*name", "trailing. "} {
		if _, err := validateBackupLabel(invalid); err == nil {
			t.Fatalf("label %q should be rejected", invalid)
		}
	}
	if label, err := validateBackupLabel("\x00bad"); err != nil || label != "bad" {
		t.Fatalf("null characters should be removed: %q, %v", label, err)
	}
}

func TestManualBackupPromptsForLabel(t *testing.T) {
	m := newModel(nil, vaultData{}, settings{})
	m.screen, m.formValues = backupSettingsScreen, m.backupFormValues()
	updated, _ := m.updateForm(tea.KeyMsg{Type: tea.KeyCtrlB})
	result := updated.(model)
	if result.screen != backupLabelScreen || len(result.formValues) != 1 || result.formValues[0] != "" || len(result.manualBackupValues) != 5 {
		t.Fatalf("manual backup should prompt for a label: %#v", result)
	}
}

func TestIncompleteBackupSettingsFormReturnsErrorInsteadOfPanicking(t *testing.T) {
	m := newModel(nil, vaultData{}, settings{})
	if err := m.saveBackupSettingsValues([]string{""}); err == nil {
		t.Fatal("incomplete backup settings should return an error")
	}
}

func TestManualBackupEmptyLabelDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	m := newModel(&store{dir: dir, vaultPath: filepath.Join(dir, "vault.json"), settingsPath: filepath.Join(dir, "settings.json")}, vaultData{}, settings{})
	m.screen, m.formValues = backupSettingsScreen, m.backupFormValues()
	updated, _ := m.updateForm(tea.KeyMsg{Type: tea.KeyCtrlB})
	updated, _ = updated.(model).updateForm(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(model)
	if result.screen != backupSettingsScreen || result.message == "" {
		t.Fatalf("empty manual backup result = %#v", result)
	}
}

func TestWebDAVToggleDisablesSyncAndKeepsLegacyConfigurationsEnabled(t *testing.T) {
	disabled := false
	if (webDAVConfig{URL: "https://example.test", Enabled: &disabled}).configured() {
		t.Fatal("disabled WebDAV configuration should not sync")
	}
	if !(webDAVConfig{URL: "https://example.test"}).configured() {
		t.Fatal("legacy WebDAV configuration should remain enabled")
	}
}

func TestWebDAVURLNormalizesInvisibleAndFullWidthCharacters(t *testing.T) {
	value, err := webDAVURL(webDAVConfig{URL: "\x00\x00\ufeffhttps\uff1a\uff0f\uff0fexample.test\u200b", Path: "ishell"})
	if err != nil {
		t.Fatal(err)
	}
	if value != "https://example.test/ishell" {
		t.Fatalf("normalized WebDAV URL = %q", value)
	}
}

func TestExistingWebDAVCollectionIsProbedBeforeCreation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ishell/" {
			t.Fatalf("collection request path = %q", request.URL.Path)
		}
		if request.Method == "MKCOL" {
			t.Fatal("existing collection should not be created again")
		}
		if request.Method != "PROPFIND" || request.Header.Get("Depth") != "0" {
			t.Fatalf("unexpected collection probe: %s depth=%q", request.Method, request.Header.Get("Depth"))
		}
		writer.WriteHeader(207)
	}))
	defer server.Close()
	if err := ensureWebDAVCollection(webDAVConfig{URL: server.URL, Path: "ishell"}); err != nil {
		t.Fatal(err)
	}
}

func TestWebDAVSettingsUseNestedScreenAndHaveToggle(t *testing.T) {
	s := &store{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	m := newModel(s, vaultData{WebDAV: webDAVConfig{URL: "https://example.test"}}, settings{})
	m.screen, m.formField, m.formValues = backupSettingsScreen, 3, m.backupFormValues()

	updated, _ := m.updateForm(tea.KeyMsg{Type: tea.KeyEnter})
	nested := updated.(model)
	if nested.screen != webDAVSettingsScreen || len(nested.formValues) != 8 {
		t.Fatalf("WebDAV configuration should open in its own screen: %#v", nested)
	}
	if nested.formValues[0] != "enabled" {
		t.Fatalf("legacy configuration should open as enabled, got %q", nested.formValues[0])
	}

	updated, _ = nested.updateForm(tea.KeyMsg{Type: tea.KeyLeft})
	if got := updated.(model).formValues[0]; got != "disabled" {
		t.Fatalf("toggle = %q, want disabled", got)
	}
}

func TestWebDAVFocusChangeClearsTestMessage(t *testing.T) {
	m := newModel(nil, vaultData{}, settings{})
	m.screen, m.formField = webDAVSettingsScreen, 5
	m.formValues = []string{"enabled", "https://example.test", "", "", "", "Test configuration", "Cloud backups", "Save"}
	m.message = "WebDAV test succeeded."
	updated, _ := m.updateForm(tea.KeyMsg{Type: tea.KeyDown})
	result := updated.(model)
	if result.formField != 6 || result.message != "" {
		t.Fatalf("focus change should clear test message: %#v", result)
	}
}

func TestCloudBackupsUsesCurrentWebDAVFormConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "PROPFIND" || request.URL.Path != "/ishell/" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/xml")
		writer.WriteHeader(207)
		fmt.Fprint(writer, `<multistatus xmlns="DAV:"></multistatus>`)
	}))
	defer server.Close()

	dir := t.TempDir()
	s := &store{dir: dir, vaultPath: filepath.Join(dir, "vault.json"), key: bytes.Repeat([]byte{1}, 32), salt: bytes.Repeat([]byte{2}, 16), password: true}
	m := newModel(s, vaultData{}, settings{})
	m.screen, m.formField = webDAVSettingsScreen, 6
	m.formValues = []string{"enabled", server.URL, "ishell", "", "", "Test configuration", "Cloud backups", "Save"}
	updated, command := m.updateForm(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(model).screen != webDAVBackupsScreen {
		t.Fatal("cloud backup action should open the backup list")
	}
	if message, ok := command().(webDAVBackupsMsg); !ok || message.err != nil {
		t.Fatalf("cloud backup list result = %#v", message)
	}
}

func TestWebDAVBackupCursorMovementClearsMessage(t *testing.T) {
	m := newModel(nil, vaultData{}, settings{})
	m.screen, m.cursor, m.message = webDAVBackupsScreen, 0, "WebDAV backup list failed: 400 Bad Request"
	updated, _ := m.updateWebDAVBackups(tea.KeyMsg{Type: tea.KeyDown})
	if got := updated.(model).message; got != "" {
		t.Fatalf("backup navigation should clear message, got %q", got)
	}
}

func TestRestoreReplacesVaultOnlyAfterValidation(t *testing.T) {
	dir := t.TempDir()
	s := &store{dir: dir, vaultPath: filepath.Join(dir, "vault.json"), key: bytes.Repeat([]byte{1}, 32), salt: bytes.Repeat([]byte{2}, 16), password: true}
	backupData := vaultData{Sessions: []session{{ID: "backup", Name: "Backup session"}}}
	if err := s.save(backupData); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dir, "backup")
	if err := os.Mkdir(backupDir, 0700); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(s.vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "vault.json"), contents, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.save(vaultData{Sessions: []session{{ID: "current", Name: "Current session"}}}); err != nil {
		t.Fatal(err)
	}
	restored, err := s.restore(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Sessions) != 1 || restored.Sessions[0].ID != "backup" {
		t.Fatalf("unexpected restored data: %#v", restored)
	}
	if _, err := s.restore(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing backup should not restore")
	}
}

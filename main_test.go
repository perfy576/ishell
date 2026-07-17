package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

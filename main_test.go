package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
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

func TestAskpassServerRequiresToken(t *testing.T) {
	server, err := startAskpassServer("session-secret")
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
	password, err := bufio.NewReader(connection).ReadString('t')
	if err != nil {
		t.Fatal(err)
	}
	if password != "session-secret" {
		t.Fatalf("unexpected password: %q", password)
	}
	connection.Close()
}

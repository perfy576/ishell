package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegrationWebDAVBackupRestoreAndDelete(t *testing.T) {
	if os.Getenv("ISHELL_RUN_WEB_DAV_INTEGRATION") != "1" {
		t.Skip("set ISHELL_RUN_WEB_DAV_INTEGRATION=1 to run against the configured WebDAV server")
	}
	source, err := newStore()
	if err != nil {
		t.Fatal(err)
	}
	data, err := source.unlock(nil)
	if err != nil {
		t.Fatal("unlock configured no-password vault: ", err)
	}
	if !data.WebDAV.configured() {
		t.Fatal("configured WebDAV is unavailable")
	}
	originalData := data
	if data.BackupPassword == "" {
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			t.Fatal(err)
		}
		data.BackupPassword = base64.RawURLEncoding.EncodeToString(random)
		if err := source.save(data); err != nil {
			t.Fatal("set temporary backup password: ", err)
		}
		defer func() {
			if err := source.save(originalData); err != nil {
				t.Errorf("restore original vault after integration test: %v", err)
			}
		}()
	}

	root := t.TempDir()
	localBackupDir := filepath.Join(root, "local-backups")
	label := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	backupSettings := settings{BackupDir: localBackupDir}
	if _, err := source.backup(backupSettings, data.WebDAV, data.BackupPassword, label); err != nil {
		t.Fatal("upload test backup: ", err)
	}
	archives, err := filepath.Glob(filepath.Join(localBackupDir, "*.zip"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("local test backup = %#v, %v", archives, err)
	}
	name := filepath.Base(archives[0])
	defer func() { _ = deleteWebDAVArchive(data.WebDAV, name) }()

	if err := os.RemoveAll(localBackupDir); err != nil {
		t.Fatal("delete temporary local backup directory: ", err)
	}
	archive, err := downloadWebDAVArchive(data.WebDAV, name)
	if err != nil {
		t.Fatal("download uploaded archive: ", err)
	}
	if err := os.MkdirAll(localBackupDir, 0700); err != nil {
		t.Fatal(err)
	}
	downloaded := filepath.Join(localBackupDir, name)
	if err := writePrivateFile(downloaded, archive); err != nil {
		t.Fatal(err)
	}

	targetDir := filepath.Join(root, "restored-vault")
	target := &store{dir: targetDir, vaultPath: filepath.Join(targetDir, "vault.json"), settingsPath: filepath.Join(targetDir, "settings.json"), key: bytes.Repeat([]byte{1}, 32), salt: bytes.Repeat([]byte{2}, 16)}
	if err := target.save(vaultData{}); err != nil {
		t.Fatal("initialize isolated no-password vault: ", err)
	}
	restored, err := target.restore(downloaded, []byte(data.BackupPassword))
	if err != nil {
		t.Fatal("restore downloaded archive: ", err)
	}
	if len(restored.Sessions) != len(data.Sessions) || restored.WebDAV.URL != data.WebDAV.URL || restored.BackupPassword != data.BackupPassword {
		t.Fatal("restored data does not match the source vault")
	}

	rebackupDir := filepath.Join(root, "rebackups")
	if _, err := target.backup(settings{BackupDir: rebackupDir}, restored.WebDAV, restored.BackupPassword, label+"-again"); err != nil {
		t.Fatal("backup after restore: ", err)
	}
	rebackups, err := filepath.Glob(filepath.Join(rebackupDir, "*.zip"))
	if err != nil || len(rebackups) != 1 {
		t.Fatalf("backup after restore = %#v, %v", rebackups, err)
	}
	if err := deleteWebDAVArchive(data.WebDAV, filepath.Base(rebackups[0])); err != nil {
		t.Fatal("delete re-backed-up archive: ", err)
	}
	if err := deleteWebDAVArchive(data.WebDAV, name); err != nil {
		t.Fatal("delete uploaded archive: ", err)
	}
	if archives, err := listWebDAVArchives(data.WebDAV); err != nil {
		t.Fatal("list archives after deletion: ", err)
	} else {
		for _, archive := range archives {
			if archive.Name == name {
				t.Fatal("deleted archive remains on WebDAV")
			}
		}
	}
}

func TestIntegrationWebDAVRestoreAfterLocalVaultDeletionWithNewPassword(t *testing.T) {
	if os.Getenv("ISHELL_RUN_WEB_DAV_INTEGRATION") != "1" {
		t.Skip("set ISHELL_RUN_WEB_DAV_INTEGRATION=1 to run against the configured WebDAV server")
	}
	live, err := newStore()
	if err != nil {
		t.Fatal(err)
	}
	data, err := live.unlock(nil)
	if err != nil {
		t.Fatal("unlock configured no-password vault: ", err)
	}
	if !data.WebDAV.configured() {
		t.Fatal("configured WebDAV is unavailable")
	}
	if data.BackupPassword == "" {
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			t.Fatal(err)
		}
		data.BackupPassword = base64.RawURLEncoding.EncodeToString(random)
	}

	root := t.TempDir()
	sourcePassword := []byte("integration-source-app-password")
	sourceSalt := bytes.Repeat([]byte{3}, 16)
	sourceDir := filepath.Join(root, "source-app")
	source := &store{dir: sourceDir, vaultPath: filepath.Join(sourceDir, "vault.json"), settingsPath: filepath.Join(sourceDir, "settings.json"), key: deriveKey(sourcePassword, sourceSalt), salt: sourceSalt, password: true}
	if err := source.save(data); err != nil {
		t.Fatal("create source vault: ", err)
	}
	backupDir := filepath.Join(sourceDir, "backups")
	label := fmt.Sprintf("password-transition-%d", time.Now().UnixNano())
	if _, err := source.backup(settings{BackupDir: backupDir}, data.WebDAV, data.BackupPassword, label); err != nil {
		t.Fatal("upload source backup: ", err)
	}
	archives, err := filepath.Glob(filepath.Join(backupDir, "*.zip"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("source archive = %#v, %v", archives, err)
	}
	name := filepath.Base(archives[0])
	defer func() { _ = deleteWebDAVArchive(data.WebDAV, name) }()
	if err := os.RemoveAll(sourceDir); err != nil {
		t.Fatal("delete source app data directory: ", err)
	}

	targetPassword := []byte("integration-target-app-password")
	targetSalt := bytes.Repeat([]byte{4}, 16)
	targetDir := filepath.Join(root, "target-app")
	target := &store{dir: targetDir, vaultPath: filepath.Join(targetDir, "vault.json"), settingsPath: filepath.Join(targetDir, "settings.json"), key: deriveKey(targetPassword, targetSalt), salt: targetSalt, password: true}
	if err := target.save(vaultData{WebDAV: data.WebDAV}); err != nil {
		t.Fatal("configure WebDAV on target vault: ", err)
	}
	if err := testWebDAV(data.WebDAV); err != nil {
		t.Fatal("test target WebDAV configuration: ", err)
	}
	archive, err := downloadWebDAVArchive(data.WebDAV, name)
	if err != nil {
		t.Fatal("download source archive on target: ", err)
	}
	downloaded := filepath.Join(targetDir, name)
	if err := writePrivateFile(downloaded, archive); err != nil {
		t.Fatal(err)
	}
	restored, err := target.restore(downloaded, []byte(data.BackupPassword))
	if err != nil {
		t.Fatal("restore downloaded archive on target: ", err)
	}
	if len(restored.Sessions) != len(data.Sessions) || restored.WebDAV.URL != data.WebDAV.URL {
		t.Fatal("restored target data does not match source data")
	}
	reader := &store{vaultPath: target.vaultPath}
	if _, err := reader.unlock(targetPassword); err != nil {
		t.Fatal("target vault was not re-encrypted with the new app password: ", err)
	}
	if _, err := (&store{vaultPath: target.vaultPath}).unlock(sourcePassword); err == nil {
		t.Fatal("source app password unlocked the restored target vault")
	}
}

func TestIntegrationWebDAVRestoreNoPasswordAfterLocalDataDeletion(t *testing.T) {
	if os.Getenv("ISHELL_RUN_WEB_DAV_INTEGRATION") != "1" {
		t.Skip("set ISHELL_RUN_WEB_DAV_INTEGRATION=1 to run against the configured WebDAV server")
	}
	live, err := newStore()
	if err != nil {
		t.Fatal(err)
	}
	data, err := live.unlock(nil)
	if err != nil {
		t.Fatal("unlock configured no-password vault: ", err)
	}
	if !data.WebDAV.configured() {
		t.Fatal("configured WebDAV is unavailable")
	}
	if data.BackupPassword == "" {
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			t.Fatal(err)
		}
		data.BackupPassword = base64.RawURLEncoding.EncodeToString(random)
	}

	root := t.TempDir()
	sourceDir := filepath.Join(root, "source-no-password")
	source := &store{dir: sourceDir, vaultPath: filepath.Join(sourceDir, "vault.json"), settingsPath: filepath.Join(sourceDir, "settings.json"), key: bytes.Repeat([]byte{5}, 32), salt: bytes.Repeat([]byte{6}, 16)}
	if err := source.save(data); err != nil {
		t.Fatal("create no-password source vault: ", err)
	}
	backupDir := filepath.Join(sourceDir, "backups")
	label := fmt.Sprintf("no-password-transition-%d", time.Now().UnixNano())
	if _, err := source.backup(settings{BackupDir: backupDir}, data.WebDAV, data.BackupPassword, label); err != nil {
		t.Fatal("upload no-password source backup: ", err)
	}
	archives, err := filepath.Glob(filepath.Join(backupDir, "*.zip"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("no-password source archive = %#v, %v", archives, err)
	}
	name := filepath.Base(archives[0])
	defer func() { _ = deleteWebDAVArchive(data.WebDAV, name) }()
	if err := os.RemoveAll(sourceDir); err != nil {
		t.Fatal("delete no-password source data directory: ", err)
	}

	targetDir := filepath.Join(root, "target-no-password")
	target := &store{dir: targetDir, vaultPath: filepath.Join(targetDir, "vault.json"), settingsPath: filepath.Join(targetDir, "settings.json"), key: bytes.Repeat([]byte{7}, 32), salt: bytes.Repeat([]byte{8}, 16)}
	if err := target.save(vaultData{WebDAV: data.WebDAV}); err != nil {
		t.Fatal("configure WebDAV on no-password target: ", err)
	}
	archive, err := downloadWebDAVArchive(data.WebDAV, name)
	if err != nil {
		t.Fatal("download no-password source archive: ", err)
	}
	downloaded := filepath.Join(targetDir, name)
	if err := writePrivateFile(downloaded, archive); err != nil {
		t.Fatal(err)
	}
	restored, err := target.restore(downloaded, []byte(data.BackupPassword))
	if err != nil {
		t.Fatal("restore no-password target: ", err)
	}
	if target.password || len(restored.Sessions) != len(data.Sessions) || restored.WebDAV.URL != data.WebDAV.URL {
		t.Fatal("no-password target was not restored with its own local protection")
	}
	contents, err := os.ReadFile(target.vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	file, _, err := parseVaultFile(contents)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decrypt(target.key, file); err != nil {
		t.Fatal("restored no-password vault is not encrypted with the target key: ", err)
	}
}

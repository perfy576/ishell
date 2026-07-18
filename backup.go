package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type vaultBackupMetadata struct {
	BackupVersion int    `json:"backup_version"`
	VaultVersion  int    `json:"vault_version"`
	CreatedAt     string `json:"created_at"`
	SourceHost    string `json:"source_host"`
	AppVersion    string `json:"app_version"`
	Salt          string `json:"salt"`
	Password      bool   `json:"password"`
	Backup        bool   `json:"backup,omitempty"`
	Version       int    `json:"version,omitempty"`
}

type vaultBackupPayload struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func (s *store) backup(value settings, webdav webDAVConfig, backupPassword, label string) (settings, error) {
	if len(backupPassword) == 0 {
		return value, errors.New("set a backup password before creating backups")
	}
	contents, err := os.ReadFile(s.vaultPath)
	if err != nil {
		return value, err
	}
	current, _, err := parseVaultFile(contents)
	if err != nil {
		return value, fmt.Errorf("read current vault: %w", err)
	}
	plaintext, err := decrypt(s.key, current)
	if err != nil {
		return value, errors.New("current vault could not be decrypted")
	}
	backupSalt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, backupSalt); err != nil {
		return value, err
	}
	backupFile, err := encrypt(deriveKey([]byte(backupPassword), backupSalt), backupSalt, true, plaintext)
	if err != nil {
		return value, err
	}
	backupFile.Backup = true
	contents, err = json.Marshal(backupFile)
	if err != nil {
		return value, err
	}
	backupDir := normalizeBackupDirectory(value.BackupDir)
	if backupDir == "" {
		backupDir = s.dir
	}
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return value, err
	}
	name := webDAVArchiveName(webdav, time.Now(), label)
	archive, err := archiveVault(contents)
	if err != nil {
		return value, err
	}
	backupPath := filepath.Join(backupDir, name)
	if err := writePrivateFile(backupPath, archive); err != nil {
		return value, fmt.Errorf("write backup %q: %w", backupPath, err)
	}
	if err := pruneArchiveBackups(backupDir, value.BackupMax); err != nil {
		return value, err
	}
	if err := uploadWebDAVArchive(webdav, name, archive, value.BackupMax); err != nil {
		return value, err
	}
	value.LastBackupAt = time.Now().UTC().Format(time.RFC3339)
	return value, s.saveSettings(value)
}

func (s *store) restore(source string, password []byte) (vaultData, error) {
	contents, err := s.loadRestoreContents(source)
	if err != nil {
		return vaultData{}, err
	}
	return s.restoreVaultContents(contents, password)
}

func (s *store) loadRestoreContents(source string) ([]byte, error) {
	path := strings.TrimSpace(source)
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		path = filepath.Join(path, "vault.json")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		contents, err = extractVaultArchive(contents)
		if err != nil {
			return nil, err
		}
	}
	if _, _, err := parseVaultFile(contents); err != nil {
		return nil, fmt.Errorf("read backup vault: %w", err)
	}
	return contents, nil
}

func (s *store) restoreVaultContents(contents, password []byte) (vaultData, error) {
	file, salt, err := parseVaultFile(contents)
	if err != nil {
		return vaultData{}, fmt.Errorf("read backup vault: %w", err)
	}
	if file.Backup {
		if len(password) == 0 {
			return vaultData{}, errors.New("a backup password is required")
		}
		plaintext, err := decrypt(deriveKey(password, salt), file)
		if err != nil {
			return vaultData{}, errors.New("backup password is incorrect or data is damaged")
		}
		var data vaultData
		if err := json.Unmarshal(plaintext, &data); err != nil {
			return vaultData{}, fmt.Errorf("decode backup vault: %w", err)
		}
		if err := s.save(data); err != nil {
			return vaultData{}, err
		}
		return data, nil
	}
	var key []byte
	if !file.Password {
		key, err = legacySystemKey()
		if err != nil {
			return vaultData{}, err
		}
	} else {
		if len(password) == 0 {
			return vaultData{}, errors.New("a legacy vault password is required")
		}
		key = deriveKey(password, salt)
	}
	plaintext, err := decrypt(key, file)
	if err != nil {
		return vaultData{}, errors.New("vault password is incorrect or data is damaged")
	}
	var data vaultData
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return vaultData{}, fmt.Errorf("decode backup vault: %w", err)
	}
	if err := s.save(data); err != nil {
		return vaultData{}, err
	}
	return data, nil
}

func (s *store) backupIfDue(value settings, webdav webDAVConfig, backupPassword string) (settings, error) {
	if value.BackupHours <= 0 {
		return value, nil
	}
	last, err := time.Parse(time.RFC3339, value.LastBackupAt)
	if err == nil && time.Since(last) < time.Duration(value.BackupHours)*time.Hour {
		return value, nil
	}
	return s.backup(value, webdav, backupPassword, "auto")
}

func archiveVault(contents []byte) ([]byte, error) {
	var vault vaultFile
	if err := json.Unmarshal(contents, &vault); err != nil {
		return nil, fmt.Errorf("read vault for backup: %w", err)
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "local"
	}
	metadata, err := json.Marshal(vaultBackupMetadata{BackupVersion: 1, VaultVersion: vault.Version, CreatedAt: time.Now().UTC().Format(time.RFC3339), SourceHost: host, AppVersion: appVersion, Salt: vault.Salt, Password: vault.Password, Backup: vault.Backup})
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(vaultBackupPayload{Nonce: vault.Nonce, Ciphertext: vault.Ciphertext})
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, entry := range []struct {
		name     string
		contents []byte
	}{{name: "metadata.json", contents: metadata}, {name: "vault.enc", contents: payload}} {
		writer, err := archive.Create(entry.name)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(entry.contents); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func extractVaultArchive(contents []byte) ([]byte, error) {
	archive, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		return nil, err
	}
	entries := make(map[string][]byte)
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() || (entry.Name != "vault.json" && entry.Name != "metadata.json" && entry.Name != "vault.enc") {
			continue
		}
		if _, found := entries[entry.Name]; found {
			return nil, errors.New("backup archive contains duplicate vault data")
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, err
		}
		vault, readErr := io.ReadAll(io.LimitReader(reader, 16<<20))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		entries[entry.Name] = vault
	}
	if vault, found := entries["vault.json"]; found {
		return vault, nil
	}
	metadata, metadataFound := entries["metadata.json"]
	payload, payloadFound := entries["vault.enc"]
	if !metadataFound || !payloadFound {
		return nil, errors.New("backup archive does not contain vault metadata and ciphertext")
	}
	var header vaultBackupMetadata
	if err := json.Unmarshal(metadata, &header); err != nil {
		return nil, fmt.Errorf("read backup metadata: %w", err)
	}
	var encrypted vaultBackupPayload
	if err := json.Unmarshal(payload, &encrypted); err != nil {
		return nil, fmt.Errorf("read backup ciphertext: %w", err)
	}
	if header.BackupVersion > 1 {
		return nil, fmt.Errorf("unsupported backup archive version %d", header.BackupVersion)
	}
	vaultVersion := header.VaultVersion
	if vaultVersion == 0 {
		vaultVersion = header.Version
	}
	return json.Marshal(vaultFile{Version: vaultVersion, Salt: header.Salt, Password: header.Password, Backup: header.Backup, Nonce: encrypted.Nonce, Ciphertext: encrypted.Ciphertext})
}

func pruneBackups(root string, maximum int) error {
	if maximum <= 0 {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && isBackupName(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for len(names) > maximum {
		if err := os.RemoveAll(filepath.Join(root, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

func pruneArchiveBackups(root string, maximum int) error {
	if maximum <= 0 {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".zip") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for len(names) > maximum {
		if err := os.Remove(filepath.Join(root, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

func isBackupName(name string) bool {
	stamp, found := strings.CutPrefix(name, "ishell-")
	if !found {
		return false
	}
	_, err := time.Parse("20060102-150405", stamp)
	return err == nil
}

func normalizeBackupDirectory(value string) string {
	return strings.TrimSpace(removeNullCharacters(value))
}

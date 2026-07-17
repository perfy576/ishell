package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/argon2"
	"golang.org/x/term"
)

const (
	appName      = "ishell"
	vaultVersion = 1
	keyringUser  = "vault-key"
)

var appVersion = "1.0.2"

type vaultFile struct {
	Version    int    `json:"version"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
	Password   bool   `json:"password"`
}

type session struct {
	ID           string `json:"id"`
	GroupID      string `json:"group_id"`
	Name         string `json:"name"`
	Protocol     string `json:"protocol"`
	Host         string `json:"host"`
	User         string `json:"user"`
	Port         string `json:"port"`
	Password     string `json:"password"`
	InitScriptID string `json:"init_script_id"`
	Created      string `json:"created"`
}

type initScript struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Interpreter string `json:"interpreter"`
	Content     string `json:"content"`
}

type commandGroup struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
}

type quickCommand struct {
	ID        string `json:"id"`
	GroupID   string `json:"group_id"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	Platform  string `json:"platform"`
	CreatedAt string `json:"created_at"`
}

type group struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
}

type vaultData struct {
	Groups        []group        `json:"groups"`
	Sessions      []session      `json:"sessions"`
	Scripts       []initScript   `json:"scripts"`
	CommandGroups []commandGroup `json:"command_groups"`
	Commands      []quickCommand `json:"commands"`
	WebDAV        webDAVConfig   `json:"webdav"`
}

type webDAVConfig struct {
	Enabled  *bool  `json:"enabled,omitempty"`
	URL      string `json:"url"`
	Path     string `json:"path"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type settings struct {
	BackupDir    string `json:"backup_dir"`
	BackupHours  int    `json:"backup_hours"`
	BackupMax    int    `json:"backup_max"`
	Language     string `json:"language"`
	LastBackupAt string `json:"last_backup_at"`
}

func migrateLegacyCommandPlatforms(data *vaultData) bool {
	changed := false
	for index := range data.Commands {
		if data.Commands[index].Platform == "" {
			data.Commands[index].Platform = runtime.GOOS
			changed = true
		}
	}
	return changed
}

type store struct {
	dir          string
	vaultPath    string
	settingsPath string
	key          []byte
	password     bool
	salt         []byte
}

func newStore() (*store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".ishell")
	return &store{dir: dir, vaultPath: filepath.Join(dir, "vault.json"), settingsPath: filepath.Join(dir, "settings.json")}, nil
}

func (s *store) exists() bool {
	_, err := os.Stat(s.vaultPath)
	return err == nil
}

func (s *store) initialize(password []byte) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	s.salt = make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, s.salt); err != nil {
		return err
	}
	s.password = len(password) > 0
	if s.password {
		s.key = deriveKey(password, s.salt)
	} else {
		s.key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, s.key); err != nil {
			return err
		}
		if err := keyring.Set(appName, keyringUser, base64.StdEncoding.EncodeToString(s.key)); err != nil {
			return fmt.Errorf("no-password mode requires an available system credential store: %w", err)
		}
	}
	return s.save(vaultData{})
}

func (s *store) unlock(password []byte) (vaultData, error) {
	contents, err := os.ReadFile(s.vaultPath)
	if err != nil {
		return vaultData{}, err
	}
	var file vaultFile
	if err := json.Unmarshal(contents, &file); err != nil {
		return vaultData{}, fmt.Errorf("read vault: %w", err)
	}
	if file.Version != vaultVersion {
		return vaultData{}, fmt.Errorf("unsupported vault version %d", file.Version)
	}
	salt, err := base64.StdEncoding.DecodeString(file.Salt)
	if err != nil {
		return vaultData{}, errors.New("vault salt is invalid")
	}
	s.salt, s.password = salt, file.Password
	if file.Password {
		if len(password) == 0 {
			return vaultData{}, errors.New("a password is required")
		}
		s.key = deriveKey(password, salt)
	} else {
		encoded, err := keyring.Get(appName, keyringUser)
		if err != nil {
			return vaultData{}, fmt.Errorf("read vault key from system credential store: %w", err)
		}
		s.key, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(s.key) != 32 {
			return vaultData{}, errors.New("system credential store returned an invalid vault key")
		}
	}
	plaintext, err := decrypt(s.key, file)
	if err != nil {
		return vaultData{}, errors.New("could not unlock vault: password may be incorrect or data damaged")
	}
	var data vaultData
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return vaultData{}, fmt.Errorf("decode vault: %w", err)
	}
	return data, nil
}

func (s *store) save(data vaultData) error {
	if len(s.key) != 32 || len(s.salt) == 0 {
		return errors.New("vault is locked")
	}
	plaintext, err := json.Marshal(data)
	if err != nil {
		return err
	}
	file, err := encrypt(s.key, s.salt, s.password, plaintext)
	if err != nil {
		return err
	}
	contents, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(s.vaultPath, contents)
}

func (s *store) readSettings() (settings, error) {
	var value settings
	contents, err := os.ReadFile(s.settingsPath)
	if errors.Is(err, os.ErrNotExist) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(contents, &value); err != nil {
		return value, err
	}
	normalized := normalizeBackupDirectory(value.BackupDir)
	if normalized != value.BackupDir {
		value.BackupDir = normalized
		if err := s.saveSettings(value); err != nil {
			return value, err
		}
	}
	return value, nil
}

func (s *store) saveSettings(value settings) error {
	contents, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(s.settingsPath, contents)
}

func (s *store) backup(value settings, webdav webDAVConfig, label string) (settings, error) {
	contents, err := os.ReadFile(s.vaultPath)
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
	if err := writePrivateFile(filepath.Join(backupDir, name), archive); err != nil {
		return value, err
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

func (s *store) restore(source string) (vaultData, error) {
	path := strings.TrimSpace(source)
	info, err := os.Stat(path)
	if err != nil {
		return vaultData{}, err
	}
	if info.IsDir() {
		path = filepath.Join(path, "vault.json")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return vaultData{}, err
	}
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		contents, err = extractVaultArchive(contents)
		if err != nil {
			return vaultData{}, err
		}
	}
	return s.restoreVaultContents(contents)
}

func (s *store) restoreVaultContents(contents []byte) (vaultData, error) {
	var file vaultFile
	if err := json.Unmarshal(contents, &file); err != nil {
		return vaultData{}, fmt.Errorf("read backup vault: %w", err)
	}
	if file.Version != vaultVersion || file.Password != s.password {
		return vaultData{}, errors.New("backup vault is incompatible with the current vault")
	}
	plaintext, err := decrypt(s.key, file)
	if err != nil {
		return vaultData{}, errors.New("backup cannot be unlocked with the current vault key")
	}
	var data vaultData
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return vaultData{}, fmt.Errorf("decode backup vault: %w", err)
	}
	if err := writePrivateFile(s.vaultPath, contents); err != nil {
		return vaultData{}, err
	}
	return data, nil
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

func (s *store) backupIfDue(value settings, webdav webDAVConfig) (settings, error) {
	if value.BackupHours <= 0 {
		return value, nil
	}
	last, err := time.Parse(time.RFC3339, value.LastBackupAt)
	if err == nil && time.Since(last) < time.Duration(value.BackupHours)*time.Hour {
		return value, nil
	}
	return s.backup(value, webdav, "auto")
}

func normalizeBackupDirectory(value string) string {
	return strings.TrimSpace(removeNullCharacters(value))
}

func removeNullCharacters(value string) string {
	return strings.ReplaceAll(value, "\x00", "")
}

func deriveKey(password, salt []byte) []byte {
	return argon2.IDKey(password, salt, 3, 64*1024, 4, 32)
}

func encrypt(key, salt []byte, password bool, plaintext []byte) (vaultFile, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return vaultFile{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return vaultFile{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return vaultFile{}, err
	}
	return vaultFile{Version: vaultVersion, Salt: base64.StdEncoding.EncodeToString(salt), Nonce: base64.StdEncoding.EncodeToString(nonce), Ciphertext: base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, plaintext, nil)), Password: password}, nil
}

func decrypt(key []byte, file vaultFile) ([]byte, error) {
	nonce, err := base64.StdEncoding.DecodeString(file.Nonce)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(file.Ciphertext)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func writePrivateFile(path string, contents []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(contents); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func readPassword(prompt string) ([]byte, error) {
	fmt.Fprint(os.Stderr, prompt)
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return password, err
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "install" {
		runInstall()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "uninstall" {
		runUninstall()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "add-command" {
		runAddCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "update" {
		runUpdate()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "break" {
		runBreak()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "telnet" {
		runTelnet(os.Args[2:])
		return
	}
	if os.Getenv(askpassAddressEnv) != "" && os.Getenv(askpassTokenEnv) != "" {
		runAskpass()
		return
	}

	s, err := newStore()
	if err != nil {
		fatal(err)
	}

	var data vaultData
	language := systemLanguage()
	if !s.exists() {
		fmt.Println(translate(language, "vault_location") + s.dir)
		password, err := readPassword(translate(language, "set_vault_password"))
		if err != nil {
			fatal(err)
		}
		if err := s.initialize(password); err != nil {
			fatal(err)
		}
		data = vaultData{}
	} else {
		contents, err := os.ReadFile(s.vaultPath)
		if err != nil {
			fatal(err)
		}
		var header vaultFile
		if err := json.Unmarshal(contents, &header); err != nil {
			fatal(err)
		}
		var password []byte
		if header.Password {
			password, err = readPassword(translate(language, "unlock_vault"))
			if err != nil {
				fatal(err)
			}
		}
		data, err = s.unlock(password)
		if err != nil {
			fatal(err)
		}
	}
	if migrateLegacyCommandPlatforms(&data) {
		if err := s.save(data); err != nil {
			fatal(err)
		}
	}
	value, err := s.readSettings()
	if err != nil {
		fatal(err)
	}
	if value, err = s.backupIfDue(value, data.WebDAV, data.BackupPassword); err != nil {
		fmt.Fprintln(os.Stderr, "backup skipped:", err)
	}
	if _, err := tea.NewProgram(newModel(s, data, value), tea.WithAltScreen()).Run(); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ishell:", err)
	os.Exit(1)
}

func runAddCommand(args []string) {
	if len(args) != 2 {
		fatal(fmt.Errorf("usage: ishell.exe add-command <name> <command>"))
	}
	s, err := newStore()
	if err != nil {
		fatal(err)
	}
	if !s.exists() {
		fatal(fmt.Errorf("initialize iShell before adding commands"))
	}
	contents, err := os.ReadFile(s.vaultPath)
	if err != nil {
		fatal(err)
	}
	var header vaultFile
	if err := json.Unmarshal(contents, &header); err != nil {
		fatal(err)
	}
	var password []byte
	if header.Password {
		password, err = readPassword(translate(systemLanguage(), "unlock_vault"))
		if err != nil {
			fatal(err)
		}
	}
	data, err := s.unlock(password)
	if err != nil {
		fatal(err)
	}
	name, command := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
	if name == "" || command == "" {
		fatal(fmt.Errorf("command name and content are required"))
	}
	updated := quickCommand{ID: newID(), Name: name, Command: command, Platform: runtime.GOOS, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	for index, value := range data.Commands {
		if value.GroupID == "" && strings.EqualFold(value.Name, name) {
			updated.ID, updated.GroupID, updated.CreatedAt = value.ID, value.GroupID, value.CreatedAt
			data.Commands[index] = updated
			if err := s.save(data); err != nil {
				fatal(err)
			}
			fmt.Println("Updated quick command: " + name)
			return
		}
	}
	data.Commands = append(data.Commands, updated)
	if err := s.save(data); err != nil {
		fatal(err)
	}
	fmt.Println("Added quick command: " + name)
}

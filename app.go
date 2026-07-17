package main

import (
	"encoding/json"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
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
	value, err := s.readSettings()
	if err != nil {
		fatal(err)
	}
	if value, err = s.backupIfDue(value, data.WebDAV); err != nil {
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

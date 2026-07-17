//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
)

func installExecutable() (string, error) {
	return "", errors.New("install is currently supported on Windows only")
}

func uninstallExecutable() (string, error) {
	return "", errors.New("uninstall is currently supported on Windows only")
}

func installedExecutablePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ishell", "bin", "ishell"), nil
}

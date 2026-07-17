//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

func installExecutable() (string, error) {
	target, err := installedExecutablePath()
	if err != nil {
		return "", err
	}
	source, err := os.Executable()
	if err != nil {
		return "", err
	}
	if !samePath(source, target) {
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return "", err
		}
		if err := copyExecutable(source, target); err != nil {
			return "", err
		}
	}
	if err := updateUserPath(filepath.Dir(target), true); err != nil {
		return "", err
	}
	return target, nil
}

func uninstallExecutable() (string, error) {
	target, err := installedExecutablePath()
	if err != nil {
		return "", err
	}
	if err := updateUserPath(filepath.Dir(target), false); err != nil {
		return "", err
	}
	source, err := os.Executable()
	if err != nil {
		return "", err
	}
	if samePath(source, target) {
		if err := scheduleSelfRemoval(target); err != nil {
			return "", err
		}
		return "iShell removed from your user PATH. The installed executable will be deleted after this process exits.", nil
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	_ = os.Remove(filepath.Dir(target))
	return "iShell removed from your user PATH.", nil
}

func scheduleSelfRemoval(target string) error {
	script, err := os.CreateTemp("", "ishell-uninstall-*.cmd")
	if err != nil {
		return err
	}
	scriptPath := script.Name()
	directory := filepath.Dir(target)
	contents := fmt.Sprintf("@echo off\r\ntimeout /t 1 /nobreak >nul\r\ndel /f /q \"%s\"\r\nrmdir \"%s\" 2>nul\r\ndel /f /q \"%%~f0\"\r\n", target, directory)
	if _, err := script.WriteString(contents); err != nil {
		script.Close()
		os.Remove(scriptPath)
		return err
	}
	if err := script.Close(); err != nil {
		os.Remove(scriptPath)
		return err
	}
	command := exec.Command("cmd.exe", "/C", scriptPath)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := command.Start(); err != nil {
		os.Remove(scriptPath)
		return err
	}
	return nil
}

func installedExecutablePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ishell", "bin", "ishell.exe"), nil
}

func copyExecutable(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temporary := target + ".new"
	output, err := os.Create(temporary)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(temporary)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return closeErr
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, target)
}

func updateUserPath(directory string, add bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	path, valueType, err := key.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	entries := strings.Split(path, ";")
	updated := make([]string, 0, len(entries)+1)
	found := false
	for _, entry := range entries {
		if entry == "" {
			continue
		}
		if samePath(entry, directory) {
			found = true
			if !add {
				continue
			}
		}
		updated = append(updated, entry)
	}
	if add && !found {
		updated = append(updated, directory)
	}
	value := strings.Join(updated, ";")
	if valueType == registry.EXPAND_SZ {
		return key.SetExpandStringValue("Path", value)
	}
	return key.SetStringValue("Path", value)
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

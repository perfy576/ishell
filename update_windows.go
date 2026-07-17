//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func replaceRunningExecutable(staged, target string) (string, error) {
	script, err := os.CreateTemp("", "ishell-update-*.cmd")
	if err != nil {
		return "", err
	}
	scriptPath := script.Name()
	contents := fmt.Sprintf("@echo off\r\ntimeout /t 1 /nobreak >nul\r\nmove /y \"%s\" \"%s\" >nul\r\ndel /f /q \"%%~f0\"\r\n", staged, target)
	if _, err := script.WriteString(contents); err != nil {
		script.Close()
		os.Remove(scriptPath)
		return "", err
	}
	if err := script.Close(); err != nil {
		os.Remove(scriptPath)
		return "", err
	}
	command := exec.Command("cmd.exe", "/C", scriptPath)
	command.Dir = filepath.Dir(target)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := command.Start(); err != nil {
		os.Remove(scriptPath)
		return "", err
	}
	return "Update downloaded. The selected iShell executable will be replaced after this process exits.", nil
}

func updateDestination(current string) (string, error) {
	installed, err := installedExecutablePath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(installed); err == nil {
		return installed, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return current, nil
}

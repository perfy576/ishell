//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func breakAllProcesses() (string, error) {
	script, err := os.CreateTemp("", "ishell-break-*.cmd")
	if err != nil {
		return "", err
	}
	scriptPath := script.Name()
	contents := fmt.Sprintf("@echo off\r\ntimeout /t 1 /nobreak >nul\r\ntaskkill /f /t /im %s.exe >nul 2>nul\r\ndel /f /q \"%%~f0\"\r\n", appName)
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
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := command.Start(); err != nil {
		os.Remove(scriptPath)
		return "", err
	}
	return "iShell sessions will be forcefully disconnected.", nil
}

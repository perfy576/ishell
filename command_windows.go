//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func launchDetachedQuickCommand(commandLine string) error {
	executable, arguments, scriptPath, err := detachedQuickCommandScript(commandLine)
	if err != nil {
		return err
	}
	commandLine = "start \"\" " + quoteCmdArgument(executable)
	for _, argument := range arguments {
		commandLine += " " + quoteCmdArgument(argument)
	}
	command := exec.Command("cmd.exe", "/D", "/S", "/C", commandLine)
	if err := command.Start(); err != nil {
		_ = os.Remove(scriptPath)
		return err
	}
	return nil
}

func detachedQuickCommandScript(commandLine string) (string, []string, string, error) {
	if strings.Contains(commandLine, "&&") || strings.Contains(commandLine, "||") {
		scriptPath, err := writeDetachedCommandScript(".cmd", "@echo off\r\n"+commandLine+"\r\ndel /f /q \"%~f0\"\r\n")
		if err != nil {
			return "", nil, "", err
		}
		return "cmd.exe", []string{"/D", "/K", scriptPath}, scriptPath, nil
	}
	powerShell := "powershell.exe"
	if candidate, err := exec.LookPath("pwsh.exe"); err == nil {
		powerShell = candidate
	}
	scriptPath, err := writeDetachedCommandScript(".ps1", commandLine+"\r\nRemove-Item -LiteralPath $PSCommandPath -Force\r\n")
	if err != nil {
		return "", nil, "", err
	}
	return powerShell, []string{"-NoLogo", "-NoExit", "-ExecutionPolicy", "Bypass", "-File", scriptPath}, scriptPath, nil
}

func writeDetachedCommandScript(extension, contents string) (string, error) {
	file, err := os.CreateTemp("", "ishell-command-*"+extension)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.WriteString(contents); err != nil {
		file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func quoteCmdArgument(value string) string {
	return "\"" + strings.ReplaceAll(filepath.Clean(value), "\"", "\"\"") + "\""
}

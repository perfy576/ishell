//go:build darwin

package main

import (
	"os"
	"os/exec"
	"strings"
)

func launchDetachedQuickCommand(commandLine string) error {
	scriptPath, err := detachedUnixCommandScript(commandLine)
	if err != nil {
		return err
	}
	terminalCommand := "/bin/sh " + posixCommandArgument(scriptPath)
	appleScript := "tell application \"Terminal\" to do script \"" + strings.ReplaceAll(strings.ReplaceAll(terminalCommand, "\\", "\\\\"), "\"", "\\\"") + "\""
	command := exec.Command("osascript", "-e", appleScript)
	if err := command.Start(); err != nil {
		_ = os.Remove(scriptPath)
		return err
	}
	return nil
}

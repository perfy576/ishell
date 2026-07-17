//go:build linux

package main

import (
	"errors"
	"os"
	"os/exec"
)

func launchDetachedQuickCommand(commandLine string) error {
	scriptPath, err := detachedUnixCommandScript(commandLine)
	if err != nil {
		return err
	}
	for _, terminal := range []struct {
		name string
		args []string
	}{
		{"x-terminal-emulator", []string{"-e", "/bin/sh", scriptPath}},
		{"gnome-terminal", []string{"--", "/bin/sh", scriptPath}},
		{"konsole", []string{"-e", "/bin/sh", scriptPath}},
		{"xfce4-terminal", []string{"-x", "/bin/sh", scriptPath}},
		{"xterm", []string{"-e", "/bin/sh", scriptPath}},
	} {
		path, lookupErr := exec.LookPath(terminal.name)
		if lookupErr != nil {
			continue
		}
		if err := exec.Command(path, terminal.args...).Start(); err == nil {
			return nil
		}
	}
	_ = os.Remove(scriptPath)
	return errors.New("no supported Linux terminal emulator was found")
}

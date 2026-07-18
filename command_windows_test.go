//go:build windows

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDetachedQuickCommandScriptsUseTheRightShell(t *testing.T) {
	executable, arguments, scriptPath, err := detachedQuickCommandScript("cd /d C:\\work && codex")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(scriptPath)
	contents, err := os.ReadFile(scriptPath)
	if err != nil || executable != "cmd.exe" || !slices.Contains(arguments, "/K") || !strings.Contains(string(contents), "cd /d C:\\work && codex") {
		t.Fatalf("cmd script = %q %#v %q %v", executable, arguments, contents, err)
	}

	executable, arguments, scriptPath, err = detachedQuickCommandScript("ls")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(scriptPath)
	contents, err = os.ReadFile(scriptPath)
	if err != nil || filepath.Ext(scriptPath) != ".ps1" || !slices.Contains(arguments, "-NoExit") || !strings.Contains(string(contents), "ls") {
		t.Fatalf("PowerShell script = %q %#v %q %v", executable, arguments, contents, err)
	}
}

func TestDetachedQuickCommandPreservesCmdArguments(t *testing.T) {
	command := detachedQuickCommand("cmd.exe", "/D", "/K", `C:\Users\test user\command.cmd`)
	want := []string{"cmd.exe", "/D", "/K", `C:\Users\test user\command.cmd`}
	if !slices.Equal(command.Args, want) {
		t.Fatalf("command args = %#v, want %#v", command.Args, want)
	}
	attributes := command.SysProcAttr
	if attributes == nil || attributes.CreationFlags != createNewConsole {
		t.Fatalf("process attributes = %#v", command.SysProcAttr)
	}
}

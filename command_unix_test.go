//go:build darwin || linux

package main

import (
	"os"
	"strings"
	"testing"
)

func TestDetachedUnixCommandScriptKeepsTerminalOpen(t *testing.T) {
	path, err := detachedUnixCommandScript("echo ready")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	contents, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(contents), "echo ready") || !strings.Contains(string(contents), "exec \"${SHELL:-/bin/sh}\" -i") {
		t.Fatalf("script = %q, %v", contents, err)
	}
}

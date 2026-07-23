//go:build !windows

package main

import (
	"slices"
	"testing"
)

func TestBreakProcessIDsIncludesIShellProcessTrees(t *testing.T) {
	processes := []breakProcess{
		{pid: 1, parent: 0, command: "/usr/local/bin/ishell"},
		{pid: 2, parent: 1, command: "/bin/zsh"},
		{pid: 3, parent: 2, command: "/usr/bin/ssh"},
		{pid: 4, parent: 2, command: "/tmp/ishell"},
		{pid: 5, parent: 4, command: "/bin/sh"},
		{pid: 6, parent: 0, command: "ishell"},
		{pid: 7, parent: 6, command: "/bin/bash"},
		{pid: 8, parent: 0, command: "/usr/local/bin/ishell-helper"},
	}
	if got, want := breakProcessIDs(processes), []int{1, 2, 3, 4, 5, 6, 7}; !slices.Equal(got, want) {
		t.Fatalf("break process IDs = %v, want %v", got, want)
	}
}

func TestParseBreakProcesses(t *testing.T) {
	processes := parseBreakProcesses("  12  1 /usr/local/bin/ishell\ninvalid\n  13 12 /bin/zsh\n")
	if len(processes) != 2 || processes[0].pid != 12 || processes[0].parent != 1 || processes[0].command != "/usr/local/bin/ishell" {
		t.Fatalf("parsed processes = %#v", processes)
	}
}

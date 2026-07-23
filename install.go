package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func runInstall() {
	if !confirm("Install iShell to your user PATH") {
		return
	}
	path, err := installExecutable()
	if err != nil {
		fatal(err)
	}
	fmt.Println("iShell installed at " + path)
	fmt.Println("Open a new terminal and run " + filepath.Base(path) + ".")
}

func runUninstall() {
	if !confirm("Remove iShell from your user PATH") {
		return
	}
	message, err := uninstallExecutable()
	if err != nil {
		fatal(err)
	}
	fmt.Println(message)
}

func confirm(action string) bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Fprintf(os.Stderr, "%s? [y/N] ", action)
	answer, err := reader.ReadString('\n')
	if err != nil || !strings.EqualFold(strings.TrimSpace(answer), "y") {
		fmt.Fprintln(os.Stderr, "Cancelled.")
		return false
	}
	return true
}

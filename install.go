package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func runInstall() {
	if !confirmTwice("Install iShell to your user PATH") {
		return
	}
	path, err := installExecutable()
	if err != nil {
		fatal(err)
	}
	fmt.Println("iShell installed at " + path)
	fmt.Println("Open a new terminal and run ishell.exe.")
}

func runUninstall() {
	if !confirmTwice("Remove iShell from your user PATH") {
		return
	}
	message, err := uninstallExecutable()
	if err != nil {
		fatal(err)
	}
	fmt.Println(message)
}

func confirmTwice(action string) bool {
	reader := bufio.NewReader(os.Stdin)
	for index := 0; index < 2; index++ {
		fmt.Fprintf(os.Stderr, "%s? [y/N] ", action)
		answer, err := reader.ReadString('\n')
		if err != nil || !strings.EqualFold(strings.TrimSpace(answer), "y") {
			fmt.Fprintln(os.Stderr, "Cancelled.")
			return false
		}
	}
	return true
}

package main

import "fmt"

func runBreak() {
	message, err := breakAllProcesses()
	if err != nil {
		fatal(err)
	}
	fmt.Println(message)
}

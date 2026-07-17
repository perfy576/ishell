//go:build darwin

package main

import "os/exec"

func platformLocale() string {
	output, err := exec.Command("defaults", "read", "-g", "AppleLocale").Output()
	if err != nil {
		return ""
	}
	return string(output)
}

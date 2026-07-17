//go:build windows

package main

import "os/exec"

func platformLocale() string {
	output, err := exec.Command("powershell.exe", "-NoProfile", "-Command", "[cultureinfo]::CurrentUICulture.Name").Output()
	if err != nil {
		return ""
	}
	return string(output)
}

//go:build darwin || linux

package main

import (
	"os"
	"strings"
)

func detachedUnixCommandScript(commandLine string) (string, error) {
	contents := "#!/bin/sh\n" + commandLine + "\nrm -f \"$0\"\nexec \"${SHELL:-/bin/sh}\" -i\n"
	file, err := os.CreateTemp("", "ishell-command-*.sh")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Chmod(0700); err != nil {
		file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if _, err := file.WriteString(contents); err != nil {
		file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func posixCommandArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
